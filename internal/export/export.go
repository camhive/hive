// Package export produces downloadable MP4 files from the archive: a time
// span of one stream, fetched from the video engine's playback endpoint
// (no transcoding) and written atomically into the export directory. Jobs
// live in hive.db so the list survives a restart (#59); the action log
// (ADR-0011) records completions.
package export

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/store"
)

// Status of a job.
type Status string

// Job states.
const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Errors.
var (
	ErrInvalid  = errors.New("export: invalid request")
	ErrNotFound = errors.New("export: job not found")
	ErrNotReady = errors.New("export: file not ready")
)

// MaxDuration bounds one export.
const MaxDuration = 24 * time.Hour

// Request describes what to export.
type Request struct {
	Camera   string
	Stream   string // main | sub
	Start    time.Time
	Duration time.Duration
	Owner    string // account id that asked; jobs are listed per owner
}

func (r Request) validate() error {
	switch {
	case r.Camera == "" || r.Owner == "":
		return fmt.Errorf("%w: camera and owner are required", ErrInvalid)
	case r.Stream != "main" && r.Stream != "sub":
		return fmt.Errorf("%w: stream must be main or sub", ErrInvalid)
	case r.Start.IsZero():
		return fmt.Errorf("%w: start is required", ErrInvalid)
	case r.Duration <= 0 || r.Duration > MaxDuration:
		return fmt.Errorf("%w: duration must be between 1s and %s", ErrInvalid, MaxDuration)
	}
	return nil
}

// Job is the state of one export.
type Job struct {
	ID        string
	Request   Request
	Status    Status
	Error     string
	Bytes     int64
	FileName  string // suggested download name
	CreatedAt time.Time
	DoneAt    time.Time
}

// Fetch returns the fMP4 bytes for a request (the engine's playback stream).
type Fetch func(ctx context.Context, req Request) (io.ReadCloser, error)

// Vault seals exports to the archive key and opens them again (ADR-0019
// §2): the hive's archivekey.Vault.
type Vault interface {
	Recipient() age.Recipient // nil: exports are kept in clear
	Open(path string) (archive.SegmentFile, error)
}

// plainVault keeps exports in clear: encryption is off.
type plainVault struct{}

// Recipient implements Vault: nothing seals.
func (plainVault) Recipient() age.Recipient { return nil }

// Open implements Vault: files are read as they are.
func (plainVault) Open(path string) (archive.SegmentFile, error) { return archive.OpenFile(path) }

// Manager runs export jobs. Each job's worker is the only writer of its row
// once Start has stored it.
type Manager struct {
	dir    string
	fetch  Fetch
	jobs   *store.Exports
	onDone func(Job)
	keep   Keep
	vault  Vault
	// files serialises what changes a finished job's file and row: sealing
	// and removal (prune, the owner's deletion). Without it a seal could
	// write back a row that a removal had just deleted.
	files sync.Mutex
}

// Option customises a Manager.
type Option func(*Manager)

// WithOnDone is called with the final state of every job (done or failed),
// e.g. to write the action log.
func WithOnDone(f func(Job)) Option { return func(m *Manager) { m.onDone = f } }

// WithVault seals exports while encryption is on and opens sealed ones.
func WithVault(v Vault) Option { return func(m *Manager) { m.vault = v } }

// NewManager creates a Manager writing files into dir and jobs into the store.
func NewManager(dir string, fetch Fetch, jobs *store.Exports, opts ...Option) *Manager {
	m := &Manager{dir: dir, fetch: fetch, jobs: jobs, onDone: func(Job) {}, vault: plainVault{}}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Recover runs once at start: jobs that were running when the hive stopped
// cannot finish, so they are marked failed, and their partial files go.
// Running it again changes nothing.
func (m *Manager) Recover(ctx context.Context) error {
	if err := m.failInterrupted(ctx); err != nil {
		return err
	}
	return m.removePartials()
}

func (m *Manager) failInterrupted(ctx context.Context) error {
	all, err := m.jobs.All(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range all {
		if Status(row.Status) != StatusRunning {
			continue
		}
		row.Status, row.Error, row.DoneAt = string(StatusFailed), "interrupted: the hive restarted", now
		if err := m.jobs.Put(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// removePartials removes what a cut-off export or seal left: temp files.
func (m *Manager) removePartials() error {
	parts, _ := filepath.Glob(filepath.Join(m.dir, ".*.part"))    // the pattern is fixed and valid
	seals, _ := filepath.Glob(filepath.Join(m.dir, ".*.age.tmp")) // as above
	for _, p := range append(parts, seals...) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Start validates the request, stores the job and begins the export in the background.
func (m *Manager) Start(ctx context.Context, req Request) (Job, error) {
	if err := req.validate(); err != nil {
		return Job{}, err
	}
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return Job{}, err
	}
	job := Job{ID: newID(), Request: req, Status: StatusRunning, CreatedAt: time.Now().UTC(), FileName: fileName(req)}
	if err := m.jobs.Put(ctx, toRow(job)); err != nil {
		return Job{}, err
	}
	go m.run(context.WithoutCancel(ctx), job)
	return job, nil
}

func (m *Manager) run(ctx context.Context, job Job) {
	err := m.write(ctx, job)
	job.DoneAt = time.Now().UTC()
	if err != nil {
		job.Status, job.Error = StatusFailed, err.Error()
	} else {
		job.Status = StatusDone
		if st, err := os.Stat(m.path(job.ID)); err == nil {
			job.Bytes = st.Size() // as stored: sealed when encryption is on
		}
	}
	if err := m.jobs.Put(ctx, toRow(job)); err != nil {
		job.Status, job.Error = StatusFailed, "storing the result: "+err.Error()
	}
	m.onDone(job)
}

// write streams the fetch into a temp file and renames it into place, so a
// failed or interrupted export never leaves a half file under the final name.
// With encryption on it is sealed as it is written: never in clear on disk.
func (m *Manager) write(ctx context.Context, job Job) error {
	to := m.vault.Recipient()
	src, err := m.fetch(ctx, job.Request)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	tmp, err := os.CreateTemp(m.dir, "."+job.ID+"-*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := copyTo(tmp, src, to); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	out := m.plainPath(job.ID)
	if to != nil {
		out += archive.SealedExt
	}
	return os.Rename(tmpName, out)
}

// copyTo copies src to w, sealed to the archive key when to is set.
func copyTo(w io.Writer, src io.Reader, to age.Recipient) error {
	if to != nil {
		return archive.SealTo(w, src, to)
	}
	_, err := io.Copy(w, src)
	return err
}

func (m *Manager) plainPath(id string) string { return filepath.Join(m.dir, id+".mp4") }

// path is where a job's file is: sealed or, without a seal, in clear.
func (m *Manager) path(id string) string {
	if sealed := m.plainPath(id) + archive.SealedExt; fileExists(sealed) {
		return sealed
	}
	return m.plainPath(id)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Get returns a job or ErrNotFound.
func (m *Manager) Get(ctx context.Context, id string) (Job, error) {
	row, err := m.jobs.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return fromRow(row), nil
}

// List returns an owner's jobs, newest first.
func (m *Manager) List(ctx context.Context, owner string) ([]Job, error) {
	rows, err := m.jobs.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

// FilePath returns the finished file's path as stored (sealed while
// encryption is on), or ErrNotReady.
func (m *Manager) FilePath(ctx context.Context, id string) (string, error) {
	j, err := m.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if j.Status != StatusDone {
		return "", ErrNotReady
	}
	return m.path(id), nil
}

// Open opens a finished export for download: a sealed one through the
// archive key (archive.ErrLocked while it is locked), so the owner gets
// plain MP4.
func (m *Manager) Open(ctx context.Context, id string) (archive.SegmentFile, error) {
	path, err := m.FilePath(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.vault.Open(path)
}

// Seal seals the finished exports kept in clear (made before encryption was
// switched on) and stores their sealed size; it returns how many it sealed.
func (m *Manager) Seal(ctx context.Context, to age.Recipient) (int, error) {
	rows, err := m.jobs.All(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		sealed, err := m.sealOne(ctx, r.ID, to)
		if err != nil {
			return n, err
		}
		if sealed {
			n++
		}
	}
	return n, nil
}

// sealOne seals one finished export kept in clear, re-reading its row under
// the files lock: a job removed since the listing stays removed.
func (m *Manager) sealOne(ctx context.Context, id string, to age.Recipient) (bool, error) {
	m.files.Lock()
	defer m.files.Unlock()
	row, err := m.jobs.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	job := fromRow(row)
	if job.Status != StatusDone || !fileExists(m.plainPath(job.ID)) {
		return false, nil
	}
	out, err := archive.SealFile(m.plainPath(job.ID), to)
	if err != nil {
		return false, err
	}
	if st, err := os.Stat(out); err == nil {
		job.Bytes = st.Size()
	}
	return true, m.jobs.Put(ctx, toRow(job))
}

func toRow(j Job) store.ExportJob {
	return store.ExportJob{ID: j.ID, Owner: j.Request.Owner, Camera: j.Request.Camera, Stream: j.Request.Stream, Start: j.Request.Start, Duration: j.Request.Duration,
		Status: string(j.Status), Error: j.Error, Bytes: j.Bytes, FileName: j.FileName, CreatedAt: j.CreatedAt, DoneAt: j.DoneAt}
}

func fromRow(r store.ExportJob) Job {
	return Job{ID: r.ID, Request: Request{Camera: r.Camera, Stream: r.Stream, Start: r.Start, Duration: r.Duration, Owner: r.Owner},
		Status: Status(r.Status), Error: r.Error, Bytes: r.Bytes, FileName: r.FileName, CreatedAt: r.CreatedAt, DoneAt: r.DoneAt}
}

func fileName(r Request) string {
	return fmt.Sprintf("%s_%s_%s_%ds.mp4", r.Camera, r.Stream, r.Start.UTC().Format("2006-01-02T15-04-05Z"), int(r.Duration.Seconds()))
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "exp_" + hex.EncodeToString(b[:])
}
