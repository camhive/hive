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
	"time"

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

// Manager runs export jobs. Each job's worker is the only writer of its row
// once Start has stored it.
type Manager struct {
	dir    string
	fetch  Fetch
	jobs   *store.Exports
	onDone func(Job)
	keep   Keep
}

// Option customises a Manager.
type Option func(*Manager)

// WithOnDone is called with the final state of every job (done or failed),
// e.g. to write the action log.
func WithOnDone(f func(Job)) Option { return func(m *Manager) { m.onDone = f } }

// NewManager creates a Manager writing files into dir and jobs into the store.
func NewManager(dir string, fetch Fetch, jobs *store.Exports, opts ...Option) *Manager {
	m := &Manager{dir: dir, fetch: fetch, jobs: jobs, onDone: func(Job) {}}
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

func (m *Manager) removePartials() error {
	parts, _ := filepath.Glob(filepath.Join(m.dir, ".*.part")) // the pattern is fixed and valid
	for _, p := range parts {
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
			job.Bytes = st.Size()
		}
	}
	if err := m.jobs.Put(ctx, toRow(job)); err != nil {
		job.Status, job.Error = StatusFailed, "storing the result: "+err.Error()
	}
	m.onDone(job)
}

// write streams the fetch into a temp file and renames it into place, so a
// failed or interrupted export never leaves a half file under the final name.
func (m *Manager) write(ctx context.Context, job Job) error {
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
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, m.path(job.ID))
}

func (m *Manager) path(id string) string { return filepath.Join(m.dir, id+".mp4") }

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

// FilePath returns the finished file's path, or ErrNotReady.
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
