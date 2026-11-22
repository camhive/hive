package datamove

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
)

// Service is the hive's OS service as a move needs it.
type Service interface {
	// Restart restarts a running hive, which then reads its note again
	// (copying: it answers without recording); nothing when no hive runs.
	Restart() error
	// Switch makes the service run the hive from dataDir and starts it
	// there; it returns only once the hive runs. "" is the definition as
	// installed, without a move's addition. When that fails it puts the
	// previous definition back and starts the hive from it again.
	Switch(dataDir string) error
}

// Options are one move.
type Options struct {
	From         hiveconfig.Paths // where the hive runs now
	FromOverride bool             // the service runs it from there by an earlier move's addition, not as installed
	ArchiveDir   string           // archive_dir of its hive.yaml; may be outside From.Data or a link to another disk
	MinFreeBytes int64            // min_free_gb: the copied footage leaves it free
	To           string           // the new data directory, absolute
	StoreKey     []byte           // opens hive.db (hiveconfig.StoreKey)
	Service      Service
	Progress     io.Writer // what the owner reads; nil: nothing

	// The system; nil is the real one.
	Disk     func(dir string) (retention.Disk, error)
	FSType   func(dir string) (string, error)
	DeviceOf func(dir string) (uint64, error)
	Now      func() time.Time
}

// Result says what moved and what stayed behind.
type Result struct {
	AlreadyDone        bool // a previous run finished this move
	CopiedSegments     int
	CopiedBytes        int64
	SkippedSegments    int // did not fit
	SkippedBytes       int64
	SkippedNewest      time.Time // the newest footage left on the old disk
	SkippedOldest      time.Time
	UnreadableSegments int // the old disk could not read them
	UnreadableBytes    int64
	Unreadable         []string // the first few
	Unusual            []string // links and special files in the archive, not copied
	Missing            []string // files a hive may lack (an unclaimed hive has no cloud.json)
}

var errLocked = errors.New("datamove: another hive move-data is running for this hive")

// small are the hive's own files in the order they are copied: what
// cannot be made again first. The certificates belong to the claim.
func small(p hiveconfig.Paths, to string) []struct{ name, src, dst string } {
	n := hiveconfig.Paths{}.WithDataDir(to)
	return []struct{ name, src, dst string }{
		{"hive.key", p.Key, n.Key},
		{"cloud.json", p.Link, n.Link},
		{"hive.crt", p.Cert, n.Cert},
		{"cloud-ca.crt", p.CACert, n.CACert},
		{"hive.yaml", p.Config, n.Config},
		{"archive-key", filepath.Join(p.Data, "archive-key"), filepath.Join(to, "archive-key")},
		{"public.crt", p.PublicCert, n.PublicCert},
		{"public.key", p.PublicKey, n.PublicKey},
	}
}

// Move moves the hive from o.From to o.To. A run cut off anywhere can be
// repeated with the same o.To and goes on where it stopped, also once the
// service already points at o.To; a run after the move finished changes
// nothing.
func Move(ctx context.Context, o Options) (Result, error) {
	o.defaults()
	if filepath.Clean(o.To) == filepath.Clean(o.From.Data) {
		return o.resumeSwitch()
	}
	if err := o.check(); err != nil {
		return Result{}, err
	}
	unlock, err := lock(o.From.Data)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	prev, err := Read(o.From.Data)
	if err != nil {
		return Result{}, err
	}
	if done, err := o.after(prev); done || err != nil {
		return Result{AlreadyDone: done}, err
	}
	m := &mover{o: o, status: Status{State: Copying, From: o.From.Data, FromOverride: o.FromOverride, To: o.To, StartedAt: o.Now().UTC()}}
	if prev.State == Copying { // the first run saw the definition before any of this move's changes
		m.status.StartedAt, m.status.FromOverride = prev.StartedAt, prev.FromOverride
	}
	res, err := m.run(ctx)
	if err != nil {
		m.status.State, m.status.Error = Copying, err.Error()
		_ = m.save(m.status.Step) // the hive keeps not recording until the move is repeated or cancelled
		return res, err
	}
	return res, nil
}

// after says what an earlier note means for this run: done (nothing to
// do), or an error when it was a move elsewhere.
func (o *Options) after(prev Status) (bool, error) {
	switch {
	case prev.State == Done && prev.To == o.To:
		fmt.Fprintf(o.Progress, "the hive was already moved to %s\n", o.To)
		return true, nil
	case prev.State == Done:
		return false, fmt.Errorf("datamove: this hive was already moved from %s to %s; run the command there", o.From.Data, prev.To)
	case prev.State == Copying && prev.To != o.To:
		return false, fmt.Errorf("datamove: a move to %s is unfinished: run it again with that directory, or cancel it", prev.To)
	}
	return false, nil
}

// resumeSwitch is a run where the service already points at o.To: a
// previous run copied everything and was cut off while handing over.
func (o *Options) resumeSwitch() (Result, error) {
	note, err := Read(o.To)
	if err != nil {
		return Result{}, err
	}
	if note.State != Switching {
		fmt.Fprintf(o.Progress, "the hive already runs from %s\n", o.To)
		return Result{AlreadyDone: true}, nil
	}
	unlock, err := lock(o.To)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	m := &mover{o: *o, status: note}
	m.o.From = hiveconfig.Paths{}.WithDataDir(note.From)
	res := Result{SkippedSegments: note.SkippedSegments, SkippedBytes: note.SkippedBytes, UnreadableSegments: note.UnreadableSegments, UnreadableBytes: note.UnreadableBytes}
	if note.SkippedNewest != nil {
		res.SkippedNewest = *note.SkippedNewest
	}
	return res, m.finish(res)
}

func (o *Options) defaults() {
	if o.Disk == nil {
		o.Disk = retention.DiskOf
	}
	if o.FSType == nil {
		o.FSType = fsType
	}
	if o.DeviceOf == nil {
		o.DeviceOf = deviceOf
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Progress == nil {
		o.Progress = io.Discard
	}
	if o.ArchiveDir == "" {
		o.ArchiveDir = o.From.Archive
	}
	if abs, err := filepath.Abs(o.ArchiveDir); err == nil {
		o.ArchiveDir = abs
	}
}

// mover is one run of a move.
type mover struct {
	o      Options
	status Status
	saved  time.Time
}

func (m *mover) newArchive() string { return filepath.Join(m.o.To, "archive") }

// run copies in this order: the small files, a first copy of hive.db (in
// case the disk dies during the footage), exports, the footage, and
// hive.db again, with what the hive wrote meanwhile; then it fixes the
// copy up and hands the service over.
func (m *mover) run(ctx context.Context) (Result, error) {
	if err := os.MkdirAll(m.o.To, 0o750); err != nil {
		return Result{}, err
	}
	if err := m.save("stopping"); err != nil {
		return Result{}, err
	}
	if err := m.o.Service.Restart(); err != nil {
		return Result{}, fmt.Errorf("datamove: restart the hive without recording: %w", err)
	}
	var res Result
	steps := []func(context.Context, *Result) error{m.copySmall, m.copyDB, m.copyExports, m.copyArchive, m.copyDB, m.fixIndex, m.fixConfig}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := step(ctx, &res); err != nil {
			return res, err
		}
	}
	return res, m.finish(res)
}

// finish notes in the new directory that the service is being handed
// over, lets the service switch, and only once the hive runs there notes
// the move done on both sides. A failed switch leaves the new note
// switching, so a rerun or --cancel from either side knows both places.
func (m *mover) finish(res Result) error {
	m.status.State = Switching
	m.status.Step, m.status.UpdatedAt, m.status.Error = "service", m.o.Now().UTC(), ""
	if err := m.status.write(m.o.To); err != nil {
		return err
	}
	if err := m.o.Service.Switch(m.o.To); err != nil {
		m.status.Error = err.Error()
		_ = m.status.write(m.o.To)
		return fmt.Errorf("datamove: run the hive from %s: %w", m.o.To, err)
	}
	m.status.State = Done
	if err := m.status.write(m.o.To); err != nil {
		return err
	}
	if err := m.save("service"); err != nil {
		return err
	}
	m.report(res)
	return nil
}

// save writes the note into the old data directory.
func (m *mover) save(step string) error {
	m.status.Step, m.status.UpdatedAt = step, m.o.Now().UTC()
	m.saved = m.o.Now()
	return m.status.write(m.o.From.Data)
}

// progress saves the note at most every two seconds.
func (m *mover) progress(n int64) {
	m.status.CopiedBytes += n
	if m.o.Now().Sub(m.saved) >= 2*time.Second {
		_ = m.save(m.status.Step)
	}
}

// copySmall copies the hive's own files; every failure, a read error
// included, stops the move: without them the hive cannot run.
func (m *mover) copySmall(_ context.Context, res *Result) error {
	res.Missing = nil
	for _, f := range small(m.o.From, m.o.To) {
		if err := m.save(f.name); err != nil {
			return err
		}
		st, err := os.Stat(f.src)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			res.Missing = append(res.Missing, f.name)
			continue
		case err != nil:
			return fmt.Errorf("datamove: %s: %w", f.name, err)
		case st.IsDir():
			err = copyTree(f.src, f.dst, func(c copied) { m.progress(c.bytes) })
		default:
			var c copied
			c, err = copyFile(f.src, f.dst, false)
			m.progress(c.bytes)
		}
		if err != nil {
			return fmt.Errorf("datamove: %s: %w", f.name, err)
		}
		fmt.Fprintln(m.o.Progress, "copied and verified", f.name)
	}
	return nil
}

// copyDB checkpoints hive.db so the file alone holds everything, then
// copies it over whatever an earlier run left (with that copy's
// write-ahead log removed, which SQLite would otherwise replay); when the
// running hive changed the file meanwhile, it tries again.
func (m *mover) copyDB(ctx context.Context, _ *Result) error {
	if err := m.save("hive.db"); err != nil {
		return err
	}
	for try := 0; ; try++ {
		c, stable, err := m.copyDBOnce(ctx)
		switch {
		case err != nil:
			return err
		case stable:
			m.progress(c.bytes)
			fmt.Fprintln(m.o.Progress, "copied and verified hive.db")
			return nil
		case try == 3:
			return errors.New("datamove: hive.db keeps changing while it is copied")
		}
	}
}

// copyDBOnce checkpoints and copies hive.db; stable says the file did not
// change while it was copied.
func (m *mover) copyDBOnce(ctx context.Context) (copied, bool, error) {
	st, err := store.Open(ctx, m.o.From.DB, m.o.StoreKey)
	if err != nil {
		return copied{}, false, fmt.Errorf("datamove: open hive.db: %w", err)
	}
	err = st.Checkpoint(ctx)
	_ = st.Close()
	if err != nil {
		return copied{}, false, fmt.Errorf("datamove: checkpoint hive.db: %w", err)
	}
	dst := filepath.Join(m.o.To, "hive.db")
	for _, stale := range []string{dst + "-wal", dst + "-shm"} {
		if err := os.Remove(stale); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return copied{}, false, err
		}
	}
	c, err := copyFile(m.o.From.DB, dst, true)
	if err != nil {
		return copied{}, false, fmt.Errorf("datamove: hive.db: %w", err)
	}
	now, _, err := hashFile(m.o.From.DB)
	return c, err == nil && string(now) == string(c.sum), nil
}

func (m *mover) copyExports(_ context.Context, _ *Result) error {
	if err := m.save("exports"); err != nil {
		return err
	}
	err := copyTree(filepath.Join(m.o.From.Data, "exports"), filepath.Join(m.o.To, "exports"), func(c copied) { m.progress(c.bytes) })
	if err != nil {
		return fmt.Errorf("datamove: exports: %w", err)
	}
	fmt.Fprintln(m.o.Progress, "copied and verified exports")
	return nil
}

// copyArchive copies the segments that fit, newest first.
func (m *mover) copyArchive(ctx context.Context, res *Result) error {
	if err := m.save("archive"); err != nil {
		return err
	}
	l, err := listSegments(m.o.ArchiveDir)
	if err != nil {
		return fmt.Errorf("datamove: list the archive: %w", err)
	}
	if err := m.notEmptyByMistake(ctx, l); err != nil {
		return err
	}
	res.Unusual = l.unusual
	have, err := m.onNewDisk(l.segments)
	if err != nil {
		return err
	}
	disk, err := m.o.Disk(m.o.To)
	if err != nil {
		return fmt.Errorf("datamove: the new disk: %w", err)
	}
	take, skip := fit(l.segments, have, disk.Total-disk.Free, limit(disk.Total, m.o.MinFreeBytes))
	m.left(res, skip)
	m.status.PlannedBytes = m.status.CopiedBytes // the files before the archive, then the footage that fits
	for _, s := range take {
		m.status.PlannedBytes += s.size
	}
	if err := m.copySegments(ctx, l.root, take, res); err != nil {
		return err
	}
	fmt.Fprintf(m.o.Progress, "copied and verified %d archive segments (%s)\n", res.CopiedSegments, size(res.CopiedBytes))
	return nil
}

// notEmptyByMistake refuses an archive directory without footage while
// the index lists some: the archive disk is not mounted, or archive_dir
// is wrong. Moving on would leave the new hive without its index.
func (m *mover) notEmptyByMistake(ctx context.Context, l listing) error {
	if len(l.segments) > 0 {
		return nil
	}
	st, err := store.Open(ctx, m.o.From.DB, m.o.StoreKey)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	u, err := st.Segments().Usage(ctx)
	if err != nil || u.Segments == 0 {
		return err
	}
	return fmt.Errorf("datamove: %s (%s) holds no footage, but the index lists %d segments: is the archive disk mounted?", m.o.ArchiveDir, l.root, u.Segments)
}

// copySegments copies the segments; one the old disk cannot read is left
// behind and reported, and the move goes on. A failure to write the new
// disk stops it.
func (m *mover) copySegments(ctx context.Context, root string, take []segment, res *Result) error {
	res.CopiedSegments, res.CopiedBytes, res.UnreadableSegments, res.UnreadableBytes, res.Unreadable = 0, 0, 0, 0, nil
	for i, s := range take {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, err := copyFile(filepath.Join(root, s.rel), filepath.Join(m.newArchive(), s.rel), false)
		switch {
		case unreadable(err):
			res.UnreadableSegments++
			res.UnreadableBytes += s.size
			if len(res.Unreadable) < 10 {
				res.Unreadable = append(res.Unreadable, s.rel+": "+err.Error())
			}
			m.status.UnreadableSegments, m.status.UnreadableBytes = res.UnreadableSegments, res.UnreadableBytes
			continue
		case err != nil:
			return fmt.Errorf("datamove: archive %s: %w", s.rel, err)
		}
		res.CopiedSegments++
		res.CopiedBytes += c.bytes
		m.progress(c.bytes)
		if (i+1)%100 == 0 {
			fmt.Fprintf(m.o.Progress, "archive: %d of %d segments\n", i+1, len(take))
		}
	}
	return nil
}

// onNewDisk is the segments' sizes already on the new disk.
func (m *mover) onNewDisk(segs []segment) (map[string]int64, error) {
	have := map[string]int64{}
	for _, s := range segs {
		st, err := os.Stat(filepath.Join(m.newArchive(), s.rel))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		have[s.rel] = st.Size()
	}
	return have, nil
}

// left notes what stays behind.
func (m *mover) left(res *Result, skip []segment) {
	res.SkippedSegments, res.SkippedBytes, res.SkippedNewest, res.SkippedOldest = len(skip), 0, time.Time{}, time.Time{}
	for _, s := range skip {
		res.SkippedBytes += s.size
		if res.SkippedNewest.IsZero() || s.start.After(res.SkippedNewest) {
			res.SkippedNewest = s.start
		}
		if res.SkippedOldest.IsZero() || s.start.Before(res.SkippedOldest) {
			res.SkippedOldest = s.start
		}
	}
	m.status.SkippedSegments, m.status.SkippedBytes, m.status.SkippedNewest = res.SkippedSegments, res.SkippedBytes, nil
	if !res.SkippedNewest.IsZero() {
		t := res.SkippedNewest.UTC()
		m.status.SkippedNewest = &t
	}
}

// fixIndex points the copied database at the new archive and removes the
// rows of segments it does not have (they did not fit, or could not be
// read): the new hive lists only footage it has. The action log says what
// moved.
func (m *mover) fixIndex(ctx context.Context, res *Result) error {
	if err := m.save("index"); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(m.o.To, "hive.db"), m.o.StoreKey)
	if err != nil {
		return fmt.Errorf("datamove: open the new hive.db: %w", err)
	}
	defer func() { _ = st.Close() }()
	if err := m.prune(ctx, st.Segments()); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"from": m.o.From.Data, "to": m.o.To, "copied_segments": res.CopiedSegments,
		"skipped_segments": res.SkippedSegments, "skipped_bytes": res.SkippedBytes, "skipped_newest": m.status.SkippedNewest,
		"unreadable_segments": res.UnreadableSegments})
	return st.Actions().Append(ctx, store.Action{Actor: "hive", Action: "hive.data_moved", Target: "hive", Details: details})
}

// prune rebases the rows onto the new archive (from archive_dir as the
// hive wrote them, and from its real place when it is a link) and removes
// those whose file is not there.
func (m *mover) prune(ctx context.Context, segs *store.Segments) error {
	prefixes := []string{m.o.ArchiveDir}
	if real, err := filepath.EvalSymlinks(m.o.ArchiveDir); err == nil && real != m.o.ArchiveDir {
		prefixes = append(prefixes, real)
	}
	for _, p := range prefixes {
		if _, err := segs.Rebase(ctx, p, m.newArchive()); err != nil {
			return err
		}
	}
	rows, err := segs.All(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := os.Stat(r.Path); err == nil && inside(r.Path, m.newArchive()) {
			continue
		}
		if err := segs.Delete(ctx, r.Camera, r.Stream, r.Start); err != nil {
			return err
		}
	}
	return nil
}

// fixConfig points the new hive.yaml at the new archive.
func (m *mover) fixConfig(_ context.Context, _ *Result) error {
	if err := m.save("hive.yaml"); err != nil {
		return err
	}
	return hiveconfig.SaveArchiveDir(hiveconfig.Paths{}.WithDataDir(m.o.To), m.newArchive())
}

// report prints what did not come along.
func (m *mover) report(res Result) {
	out := m.o.Progress
	fmt.Fprintf(out, "the hive now runs from %s\n", m.o.To)
	if res.SkippedSegments == 0 && res.UnreadableSegments == 0 && len(res.Unusual) == 0 {
		fmt.Fprintln(out, "all footage fit on the new disk")
	}
	if res.SkippedSegments > 0 {
		fmt.Fprintf(out, "did not fit: %d segments (%s), footage from %s to %s; it stays on the old disk in %s\n",
			res.SkippedSegments, size(res.SkippedBytes), res.SkippedOldest.Local().Format("2006-01-02 15:04"), res.SkippedNewest.Local().Format("2006-01-02 15:04"), m.o.ArchiveDir)
	}
	if res.UnreadableSegments > 0 {
		fmt.Fprintf(out, "could not be read from the old disk: %d segments (%s), for example:\n  %s\n", res.UnreadableSegments, size(res.UnreadableBytes), strings.Join(res.Unreadable, "\n  "))
	}
	if len(res.Unusual) > 0 {
		fmt.Fprintf(out, "not copied, not files: %d links or special files in the archive, for example %s\n", len(res.Unusual), res.Unusual[0])
	}
	if len(res.Missing) > 0 {
		fmt.Fprintln(out, "not on the old disk either (nothing to copy):", strings.Join(res.Missing, ", "))
	}
	fmt.Fprintln(out, "nothing was deleted from the old disk")
}

func size(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
