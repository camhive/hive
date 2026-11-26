// Package recording judges whether the hive records (#172, ADR-0024
// amendment): the archive's disk is mounted where it is configured, the
// archive root is there on the device it was first seen on and takes a
// synced write; the database takes a write without SQLite's I/O,
// corruption or read-only errors; and segments keep starting while a
// camera is online. The verdict lives in memory only: it is the alarm for
// the disk the store and the archive live on, so it never goes through
// them. A Watchdog checks once a minute and tells each change.
package recording

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/camhive/hive/internal/diskhealth"
)

// Reason says why the hive does not record.
type Reason string

// Reasons, in the order they are judged: the first that holds stands.
const (
	DiskNotMounted    Reason = "disk_not_mounted"   // the configured mount point holding the archive (or the data directory) is not mounted
	ArchiveMissing    Reason = "archive_missing"    // the archive root it saw is gone (ENOENT, ENOTDIR, ESTALE)
	ArchiveChanged    Reason = "archive_changed"    // the archive root is on another device than when first seen
	ArchiveUnwritable Reason = "archive_unwritable" // the root cannot be read, or a small synced write into it failed, twice in a row
	Database          Reason = "database"           // SQLite answered with I/O, corruption or read-only errors twice in a row
	EngineStalled     Reason = "engine_stalled"     // no new segment for Stall while a camera is online, at two checks
)

// State is the recording state: recording since Since (Reason empty), or
// not recording since Since for Reason. Detail is what was seen, for the
// hive's own log and status; it carries paths and never leaves the hive.
type State struct {
	Since  time.Time
	Reason Reason
	Detail string
}

// Recording reports whether the hive records.
func (s State) Recording() bool { return s.Reason == "" }

// Timing.
const (
	Every      = time.Minute      // how often the watchdog checks
	ProbeEvery = 10 * time.Minute // the longest between two write probes of the archive and of the database (disk wear, #154)
	Confirm    = 2                // checks in a row a slow reason needs before it counts
)

// ProbeName is the file the watchdog's write probe uses in the archive root.
const ProbeName = ".recording-probe"

// Options configure a Watchdog. Zero values are the real system or off.
type Options struct {
	ArchiveDir string
	MountPoint string                          // the mount point (fstab) that must hold the archive; "" none
	Every      time.Duration                   // default Every
	ProbeEvery time.Duration                   // default ProbeEvery
	Stall      time.Duration                   // no new segment for this long while a camera is online; 0: not judged
	Store      func(context.Context) error     // a small write to the database; nil: not judged
	Newest     func(string) (time.Time, error) // the newest segment's start under the archive root; nil: not judged
	Online     func() int                      // cameras online now; nil: none
	OnChange   func(State)                     // the first check and every change between recording and not
	Logger     *slog.Logger

	// For tests.
	Now     func() time.Time // its monotonic reading measures the stall
	StatDev func(string) (uint64, error)
	Mounted func(mountPoint string) bool // default: listed in /proc/self/mountinfo (true where there is none)
	Probe   func(dir string) error
	Tick    func(time.Duration) (<-chan time.Time, func())
}

// Watchdog judges the recording state.
type Watchdog struct {
	o Options

	mu       sync.Mutex // the state and the errors others observed
	state    State
	checked  bool
	observed []error

	checking sync.Mutex // one check at a time; it owns the memory
	mem      memory
}

// New makes a Watchdog.
func New(o Options) *Watchdog {
	o.Every = cmp.Or(o.Every, Every)
	o.ProbeEvery = cmp.Or(o.ProbeEvery, ProbeEvery)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.StatDev == nil {
		o.StatDev = diskhealth.StatDev
	}
	if o.Probe == nil {
		o.Probe = func(dir string) error { return diskhealth.WriteProbe(dir, ProbeName) }
	}
	if o.Online == nil {
		o.Online = func() int { return 0 }
	}
	if o.Mounted == nil {
		o.Mounted = func(mp string) bool {
			ok, err := diskhealth.IsMountPoint("/proc/self/mountinfo", mp)
			return ok || err != nil // no mount table to read (not Linux): not judged
		}
	}
	if o.OnChange == nil {
		o.OnChange = func(State) {}
	}
	if o.Tick == nil {
		o.Tick = func(d time.Duration) (<-chan time.Time, func()) { t := time.NewTicker(d); return t.C, t.Stop }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	start := o.Now()
	return &Watchdog{o: o, mem: memory{quiet: start, advanced: start}}
}

// State is the last check's verdict; recording since the zero time before
// the first.
func (w *Watchdog) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// Observe takes a failed write another part of the hive saw (the index,
// the sealer): a disk's I/O or read-only error counts at the next check
// as a failed probe of the database (SQLite's words) or of the archive.
func (w *Watchdog) Observe(err error) {
	if !broken(err) {
		return
	}
	w.mu.Lock()
	w.observed = append(w.observed, err)
	w.mu.Unlock()
}

// Run checks now and then every Every until ctx ends.
func (w *Watchdog) Run(ctx context.Context) {
	tick, stop := w.o.Tick(w.o.Every)
	defer stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// Check reads everything due once, keeps the verdict and tells a change.
func (w *Watchdog) Check(ctx context.Context) State {
	w.checking.Lock()
	defer w.checking.Unlock()
	now := w.o.Now()
	w.mu.Lock()
	stopped, observed := !w.state.Recording(), w.observed
	w.observed = nil
	w.mu.Unlock()

	r := w.read()
	w.mem.cameras(r.online, now)
	w.mem.track(r.newest, now)
	w.measure(ctx, r, observed, stopped, now)
	reason, detail := judge(r, w.mem, w.o.MountPoint)
	w.mem.root(r)

	w.mu.Lock()
	prev, first := w.state, !w.checked
	next := step(prev, first, reason, detail, now)
	w.state, w.checked = next, true
	w.mu.Unlock()

	if first || next.Recording() != prev.Recording() {
		w.log(next)
		w.o.OnChange(next)
	}
	return next
}

// step moves the state by one verdict: an episode begins with its first
// reason and keeps it; only the detail follows later checks.
func step(prev State, first bool, reason Reason, detail string, now time.Time) State {
	switch {
	case reason == "" && (first || !prev.Recording()):
		return State{Since: now}
	case reason == "":
		return prev
	case first || prev.Recording():
		return State{Since: now, Reason: reason, Detail: detail}
	}
	prev.Detail = detail
	return prev
}

func (w *Watchdog) log(s State) {
	if s.Recording() {
		w.o.Logger.Info("recording")
		return
	}
	w.o.Logger.Error("not recording", "reason", s.Reason, "detail", s.Detail)
}

// reading is what every check reads: cheap, no write.
type reading struct {
	root    error // stat of the archive root
	dev     uint64
	mounted bool // the configured mount point is mounted (true without one)
	newest  time.Time
	online  int
}

func (w *Watchdog) read() reading {
	var r reading
	r.dev, r.root = w.o.StatDev(w.o.ArchiveDir)
	r.mounted = w.o.MountPoint == "" || w.o.Mounted(w.o.MountPoint)
	if r.root == nil && w.o.Newest != nil {
		var err error
		r.newest, err = w.o.Newest(w.o.ArchiveDir)
		if err != nil && !w.mem.newestFailed {
			w.o.Logger.Warn("newest segment unreadable", "err", err)
		}
		w.mem.newestFailed = err != nil
	}
	r.online = w.o.Online()
	return r
}

// measure runs the write probes that are due and folds what others
// observed into them; each signal moves at most once per check. The
// archive is probed at most every ProbeEvery and not when a segment
// started since the last probe (the engine wrote); the database at most
// every ProbeEvery. After a failure, and while not recording, both are
// probed at every check.
func (w *Watchdog) measure(ctx context.Context, r reading, observed []error, stopped bool, now time.Time) {
	m := &w.mem
	probe, store := w.probeArchive(r, stopped, now), w.probeStore(ctx, stopped, now)
	for _, err := range observed {
		if sqlite(err) {
			store = measurement{err: err, taken: true}
		} else {
			probe = measurement{err: err, taken: true}
		}
	}
	if probe.taken {
		m.probe.add(probe.err, now)
	}
	if store.taken {
		m.store.add(store.err, now)
	}
	m.stall.add(w.stalled(r, now), now)
}

// measurement is one probe's outcome; not taken when none was due.
type measurement struct {
	err   error
	taken bool
}

// probeArchive is the archive's measurement at this check, if one is due.
func (w *Watchdog) probeArchive(r reading, stopped bool, now time.Time) measurement {
	m := &w.mem
	switch {
	case r.root != nil && !diskhealth.Gone(r.root):
		return measurement{err: r.root, taken: true} // unreadable root (EACCES, EIO)
	case r.root != nil, !m.probe.due(now, w.o.ProbeEvery, stopped):
		return measurement{}
	case !m.probe.newest.IsZero() && m.newest.After(m.probe.newest):
		m.probe.newest = m.newest
		return measurement{taken: true} // the engine started a segment since the last probe
	}
	m.probe.newest = m.newest
	return measurement{err: w.o.Probe(w.o.ArchiveDir), taken: true}
}

// probeStore is the database's measurement at this check, if one is due;
// only a disk's error counts (a busy database is not one).
func (w *Watchdog) probeStore(ctx context.Context, stopped bool, now time.Time) measurement {
	if w.o.Store == nil || !w.mem.store.due(now, w.o.ProbeEvery, stopped) {
		return measurement{}
	}
	if err := w.o.Store(ctx); broken(err) {
		return measurement{err: err, taken: true}
	}
	return measurement{taken: true}
}

// stalled is the stall's measurement: a camera online and the newest
// segment unchanged for Stall on this watchdog's clock.
func (w *Watchdog) stalled(r reading, now time.Time) error {
	quiet := now.Sub(later(w.mem.advanced, w.mem.quiet))
	if w.o.Stall > 0 && r.online > 0 && quiet > w.o.Stall {
		return fmt.Errorf("%d cameras online and no new segment for %s", r.online, quiet.Round(time.Minute))
	}
	return nil
}

// memory is what checks remember.
type memory struct {
	seen  bool      // the archive root existed at a check where its disk was mounted
	dev   uint64    // its device number then
	quiet time.Time // since when a missing segment counts: the start, or a camera coming online
	was   int       // cameras online at the previous check

	newest       time.Time // the newest segment start seen, from the file names
	advanced     time.Time // when it last changed, on this watchdog's clock: a file name's time is never compared with now
	newestFailed bool

	probe, store, stall signal
}

// signal is a measured condition that needs Confirm failures in a row.
type signal struct {
	err    error     // the last failure; nil after a success
	streak int       // failures in a row
	at     time.Time // last measured
	newest time.Time // probe only: the newest segment at the last probe
}

// add records one measurement; a full disk is no failure here (that is
// storage.low).
func (s *signal) add(err error, now time.Time) {
	s.at = now
	if err == nil || diskhealth.Classify(err) == diskhealth.NoSpace {
		s.err, s.streak = nil, 0
		return
	}
	s.err = err
	s.streak++
}

func (s signal) due(now time.Time, every time.Duration, stopped bool) bool {
	return s.at.IsZero() || s.streak > 0 || stopped || now.Sub(s.at) >= every
}

func (s signal) confirmed() bool { return s.streak >= Confirm }

// cameras notes when a camera came online: a missing segment counts from then.
func (m *memory) cameras(online int, now time.Time) {
	if online > 0 && m.was == 0 {
		m.quiet = now
	}
	m.was = online
}

// track notes when the newest segment changed.
func (m *memory) track(newest, now time.Time) {
	if !newest.Equal(m.newest) {
		m.newest, m.advanced = newest, now
	}
}

// root keeps the archive root's device the first time it is seen with its
// disk mounted: an empty mount point on the system disk is never taken
// for the archive.
func (m *memory) root(r reading) {
	if r.root == nil && r.mounted && !m.seen {
		m.seen, m.dev = true, r.dev
	}
}

// broken says an error is one of a disk that went bad: I/O or read-only.
func broken(err error) bool {
	c := diskhealth.Classify(err)
	return c == diskhealth.IO || c == diskhealth.ReadOnly
}

// sqlite says an error is SQLite's (the database), not the file system's.
func sqlite(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "disk i/o error") || strings.Contains(s, "database") || strings.Contains(s, "store:")
}

// judge is the verdict on one check: the first reason that holds, in the
// order of the Reason constants, and what was seen.
func judge(r reading, m memory, mountPoint string) (Reason, string) {
	switch {
	case !r.mounted:
		return DiskNotMounted, mountPoint + " is not mounted: the archive would be written to the system disk"
	case r.root != nil && m.seen && diskhealth.Gone(r.root):
		return ArchiveMissing, r.root.Error()
	case r.root == nil && m.seen && r.dev != m.dev:
		return ArchiveChanged, fmt.Sprintf("the archive root is on device %#x, it was on %#x", r.dev, m.dev)
	case m.probe.confirmed():
		return ArchiveUnwritable, m.probe.err.Error()
	case m.store.confirmed():
		return Database, m.store.err.Error()
	case m.stall.confirmed():
		return EngineStalled, m.stall.err.Error()
	}
	return "", ""
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
