package recording_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/camhive/hive/internal/recording"
)

var t0 = time.Date(2026, 11, 24, 10, 29, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// host is what the watchdog reads, settable by a test.
type host struct {
	archive string
	clock   *clock
	changes []recording.State
	store   error
	probe   error
	devs    map[string]uint64 // StatDev answers by path when set; else the real file system
	newest  time.Time
	online  int
	probes  int // archive write probes
	stores  int // database write probes
}

func newHost(t *testing.T) *host {
	t.Helper()
	h := &host{archive: filepath.Join(t.TempDir(), "archive"), clock: &clock{t0}}
	if err := os.MkdirAll(h.archive, 0o750); err != nil {
		t.Fatal(err)
	}
	h.newest = t0
	return h
}

func (h *host) watchdog(opts ...func(*recording.Options)) *recording.Watchdog {
	o := recording.Options{
		ArchiveDir: h.archive, Now: h.clock.now, Stall: 10 * time.Minute,
		Store:    func(context.Context) error { h.stores++; return h.store },
		Newest:   func(string) (time.Time, error) { return h.newest, nil },
		Online:   func() int { return h.online },
		OnChange: func(s recording.State) { h.changes = append(h.changes, s) },
		Probe: func(dir string) error {
			h.probes++
			if h.probe != nil {
				return h.probe
			}
			_, err := os.Stat(dir)
			return err
		},
	}
	if h.devs != nil {
		o.StatDev = func(dir string) (uint64, error) {
			if _, err := os.Stat(dir); err != nil {
				return 0, err
			}
			return h.devs[dir], nil
		}
	}
	for _, f := range opts {
		f(&o)
	}
	return recording.New(o)
}

func (h *host) tick(w *recording.Watchdog) recording.State {
	h.clock.t = h.clock.t.Add(time.Minute)
	return w.Check(context.Background())
}

// The first check says what it found, recording or not.
func TestTheFirstCheckIsAlwaysTold(t *testing.T) {
	h := newHost(t)
	w := h.watchdog()
	if st := w.Check(context.Background()); !st.Recording() || !st.Since.Equal(t0) {
		t.Fatalf("state = %+v, want recording since t0", st)
	}
	h.tick(w)
	if len(h.changes) != 1 || !h.changes[0].Recording() {
		t.Fatalf("changes = %+v, want one: recording", h.changes)
	}
}

// #172: the archive root the watchdog saw is gone (the mount vanished):
// not recording at once, told once, and recording again when it is back.
func TestAVanishedArchiveStopsRecordingAtOnceUntilItIsBack(t *testing.T) {
	h := newHost(t)
	w := h.watchdog()
	w.Check(context.Background())
	if err := os.RemoveAll(h.archive); err != nil {
		t.Fatal(err)
	}
	st := h.tick(w)
	stoppedAt := h.clock.t
	if st.Reason != recording.ArchiveMissing || !st.Since.Equal(stoppedAt) || st.Detail == "" {
		t.Fatalf("state = %+v, want archive_missing since %v with a detail", st, stoppedAt)
	}
	h.tick(w)
	if len(h.changes) != 2 || h.changes[1].Reason != recording.ArchiveMissing {
		t.Fatalf("changes = %+v, want recording then one archive_missing", h.changes)
	}
	if got := w.State(); !got.Since.Equal(stoppedAt) {
		t.Fatalf("State() = %+v, want the episode's start kept", got)
	}
	if err := os.MkdirAll(h.archive, 0o750); err != nil {
		t.Fatal(err)
	}
	if st := h.tick(w); !st.Recording() || len(h.changes) != 3 {
		t.Fatalf("state = %+v, changes %d; want recording again, told", st, len(h.changes))
	}
}

// A hive that has never recorded has no archive root yet: no alarm.
func TestNoArchiveYetIsNotAnAlarm(t *testing.T) {
	h := newHost(t)
	_ = os.RemoveAll(h.archive)
	if st := h.watchdog().Check(context.Background()); !st.Recording() {
		t.Fatalf("state = %+v, want recording (no archive yet)", st)
	}
}

// #172: the archive root on another device than when first seen.
func TestAnArchiveOnAnotherDeviceStopsRecordingAtOnce(t *testing.T) {
	h := newHost(t)
	h.devs = map[string]uint64{h.archive: 0x811}
	w := h.watchdog()
	w.Check(context.Background())
	h.devs[h.archive] = 0xb302
	if st := h.tick(w); st.Reason != recording.ArchiveChanged {
		t.Fatalf("state = %+v, want archive_changed", st)
	}
}

// #172 review: the disk absent at boot leaves its fstab mount point as an
// empty directory on the system disk. The watchdog never takes that for
// the archive: not recording from the first check, and the device seen
// once the disk is mounted is the one it remembers. Whether it is mounted
// comes from the mount table, so a bind mount on the same device as its
// parent counts (#172 review 2).
func TestAnArchiveOnAnUnmountedMountPointIsNotRecording(t *testing.T) {
	h := newHost(t)
	mp := filepath.Dir(h.archive)
	h.devs = map[string]uint64{filepath.Dir(mp): 1, mp: 1, h.archive: 1} // one device: a bind mount, or the bare mount point
	mountedNow := false
	w := h.watchdog(func(o *recording.Options) {
		o.MountPoint = mp
		o.Mounted = func(p string) bool { return p == mp && mountedNow }
	})
	if st := w.Check(context.Background()); st.Reason != recording.DiskNotMounted {
		t.Fatalf("state = %+v, want disk_not_mounted at the first check", st)
	}
	mountedNow = true // mounted (here a bind mount: same device as its parent)
	if st := h.tick(w); !st.Recording() {
		t.Fatalf("state = %+v, want recording once mounted", st)
	}
	mountedNow = false
	if st := h.tick(w); st.Reason != recording.DiskNotMounted {
		t.Fatalf("state = %+v, want disk_not_mounted again", st)
	}
}

// A failed write probe counts only when the next check fails too; a full
// disk is storage.low's business, not this one's.
func TestAnUnwritableArchiveNeedsTwoChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want recording.Reason
	}{
		"eio":       {&os.PathError{Op: "write", Path: "probe", Err: syscall.EIO}, recording.ArchiveUnwritable},
		"read-only": {&os.PathError{Op: "open", Path: "probe", Err: syscall.EROFS}, recording.ArchiveUnwritable},
		"denied":    {&os.PathError{Op: "open", Path: "probe", Err: syscall.EACCES}, recording.ArchiveUnwritable},
		"full":      {&os.PathError{Op: "write", Path: "probe", Err: syscall.ENOSPC}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.probe = tc.err
			w := h.watchdog()
			if st := w.Check(context.Background()); !st.Recording() {
				t.Fatalf("first check = %+v, want recording until confirmed", st)
			}
			if st := h.tick(w); st.Reason != tc.want {
				t.Fatalf("second check = %+v, want %q", st, tc.want)
			}
		})
	}
}

// #172 review: a root that cannot be read (permissions changed) is
// unwritable, not recording.
func TestAnUnreadableRootIsUnwritable(t *testing.T) {
	h := newHost(t)
	denied := &os.PathError{Op: "stat", Path: "archive", Err: syscall.EACCES}
	w := h.watchdog(func(o *recording.Options) {
		o.StatDev = func(string) (uint64, error) { return 0, denied }
	})
	w.Check(context.Background())
	if st := h.tick(w); st.Reason != recording.ArchiveUnwritable {
		t.Fatalf("state = %+v, want archive_unwritable", st)
	}
}

// One failure, then a success: no episode (an intermittent USB reset does
// not mail the owner every two minutes).
func TestOneFailedProbeIsNoEpisode(t *testing.T) {
	h := newHost(t)
	h.probe = &os.PathError{Op: "write", Path: "probe", Err: syscall.EIO}
	w := h.watchdog()
	w.Check(context.Background())
	h.probe = nil
	h.tick(w)
	h.tick(w)
	if len(h.changes) != 1 || !h.changes[0].Recording() {
		t.Fatalf("changes = %+v, want only the first check's recording", h.changes)
	}
}

// SQLite's I/O, corruption and read-only answers stop recording at the
// second check; a busy database does not.
func TestABrokenDatabaseStopsRecording(t *testing.T) {
	for msg, want := range map[string]recording.Reason{
		"store: upsert segment: disk I/O error (778)": recording.Database,
		"database disk image is malformed (11)":       recording.Database,
		"attempt to write a readonly database (8)":    recording.Database,
		"database is locked (5)":                      "",
	} {
		h := newHost(t)
		h.store = errors.New(msg)
		w := h.watchdog()
		w.Check(context.Background())
		if st := h.tick(w); st.Reason != want {
			t.Errorf("store error %q: state = %+v, want reason %q", msg, st, want)
		}
	}
}

// Failed writes others saw count at the next check: SQLite's as the
// database's, the file system's as the archive's.
func TestObservedWriteErrorsCount(t *testing.T) {
	h := newHost(t)
	w := h.watchdog()
	w.Check(context.Background())
	for range 2 {
		w.Observe(errors.New("store: upsert segment: disk I/O error (778)"))
		h.tick(w)
	}
	if st := w.State(); st.Reason != recording.Database {
		t.Fatalf("state = %+v, want database", st)
	}

	h = newHost(t)
	w = h.watchdog()
	w.Check(context.Background())
	w.Observe(&os.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}) // says nothing
	for range 2 {
		w.Observe(&os.PathError{Op: "write", Path: "seg.mp4", Err: syscall.EIO})
		h.tick(w)
	}
	if st := w.State(); st.Reason != recording.ArchiveUnwritable {
		t.Fatalf("state = %+v, want archive_unwritable", st)
	}
}

// The engine starting no segment for Stall while a camera is online is
// not recording, at the second such check; without a camera online, or
// after a camera just came online, it is not judged yet.
func TestAStalledEngineStopsRecording(t *testing.T) {
	h := newHost(t)
	w := h.watchdog()
	w.Check(context.Background()) // t0, no camera online
	h.clock.t = t0.Add(time.Hour)
	if st := w.Check(context.Background()); !st.Recording() {
		t.Fatalf("state = %+v, want recording: no camera online", st)
	}
	h.online = 1 // a camera comes online after an hour; the newest segment is an hour old
	if st := h.tick(w); !st.Recording() {
		t.Fatalf("state = %+v, want recording: the camera just came online", st)
	}
	for range 10 {
		h.tick(w)
	}
	if st := h.tick(w); !st.Recording() {
		t.Fatalf("state = %+v after 11 minutes, want recording until a second check confirms", st)
	}
	if st := h.tick(w); st.Reason != recording.EngineStalled {
		t.Fatalf("state = %+v, want engine_stalled", st)
	}
	h.newest = h.newest.Add(10 * time.Minute)
	if st := h.tick(w); !st.Recording() {
		t.Fatalf("state = %+v, want recording once a segment starts", st)
	}
}

// #172 review: a Pi without a real-time clock boots on an old time and NTP
// steps it forward; the segments' file names then lie far behind the wall
// clock. A stall is measured by when the newest segment last changed, on
// the watchdog's own clock, never by subtracting a file name's time.
func TestAClockStepDoesNotLookLikeAStall(t *testing.T) {
	h := newHost(t)
	h.online = 1
	h.newest = t0.Add(-40 * time.Minute) // the files were named before the step
	w := h.watchdog()
	w.Check(context.Background())
	for i := range 30 {
		if i%9 == 0 {
			h.newest = h.newest.Add(9 * time.Minute) // a segment every 9 minutes, still named on the old clock
		}
		if st := h.tick(w); !st.Recording() {
			t.Fatalf("check %d: state = %+v, want recording", i, st)
		}
	}
}

// #172 review, disk wear (#154): an idle hive writes a probe into the
// archive and the database at most every ProbeEvery, and a hive whose
// engine keeps starting segments writes no archive probe at all.
func TestProbesAreRare(t *testing.T) {
	h := newHost(t)
	w := h.watchdog()
	for i := range 30 {
		if i > 0 {
			h.tick(w)
		} else {
			w.Check(context.Background())
		}
	}
	if h.probes != 3 || h.stores != 3 {
		t.Fatalf("30 minutes idle: %d archive and %d database probes, want 3 each", h.probes, h.stores)
	}

	h = newHost(t)
	w = h.watchdog()
	w.Check(context.Background())
	for range 30 {
		h.newest = h.newest.Add(time.Minute)
		h.tick(w)
	}
	if h.probes != 1 {
		t.Fatalf("recording hive: %d archive probes, want only the first (the engine's segments show the disk takes writes)", h.probes)
	}
}

// The first reason of an episode stands for it; a later one only changes
// the detail, and nothing new is told.
func TestAnEpisodeKeepsItsFirstReason(t *testing.T) {
	h := newHost(t)
	h.store = errors.New("database disk image is malformed (11)")
	w := h.watchdog()
	w.Check(context.Background())
	h.tick(w)
	_ = os.RemoveAll(h.archive)
	st := h.tick(w)
	if st.Reason != recording.Database || len(h.changes) != 2 {
		t.Fatalf("state = %+v, changes %d; want database kept and nothing new told", st, len(h.changes))
	}
}

// Run checks at start and at every tick.
func TestRunChecksAtStartAndEveryInterval(t *testing.T) {
	h := newHost(t)
	ticks := make(chan time.Time)
	checked := make(chan recording.State, 4)
	w := h.watchdog(func(o *recording.Options) {
		o.Tick = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
		o.OnChange = func(s recording.State) { checked <- s }
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	<-checked
	_ = os.RemoveAll(h.archive)
	ticks <- t0
	select {
	case st := <-checked:
		if st.Reason != recording.ArchiveMissing {
			t.Fatalf("state = %+v, want archive_missing", st)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no check at the tick")
	}
}
