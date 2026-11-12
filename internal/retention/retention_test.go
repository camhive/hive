package retention_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
)

// fakeFS tracks files and free space without touching a disk.
type fakeFS struct {
	mu    sync.Mutex
	files map[string]int64
	free  int64
}

func (f *fakeFS) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	size, ok := f.files[path]
	if !ok {
		return os.ErrNotExist
	}
	delete(f.files, path)
	f.free += size
	return nil
}

func (f *fakeFS) Free(string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.free, nil
}

func (f *fakeFS) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.files))
	for p := range f.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type fixture struct {
	st    *store.Store
	idx   *archive.Index
	fs    *fakeFS
	now   time.Time
	low   []int64 // storage.low notifications (free bytes)
	lowMu sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &fixture{st: st, idx: archive.NewIndex(st), fs: &fakeFS{files: map[string]int64{}, free: 1 << 40}, now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
}

// add indexes a 10-minute, 100-byte main-stream segment that started `age` ago.
func (f *fixture) add(t *testing.T, cam string, age time.Duration, complete bool) archive.Segment {
	t.Helper()
	const stream = "main"
	start := f.now.Add(-age)
	path := filepath.Join("/archive", cam, stream, start.Format("2006-01-02_15-04-05")+"-000000_Z.mp4")
	seg := archive.Segment{Camera: cam, Stream: stream, Start: start, Duration: 10 * time.Minute, Bytes: 100, Path: path, Complete: complete}
	if err := f.idx.Upsert(context.Background(), seg); err != nil {
		t.Fatal(err)
	}
	f.fs.mu.Lock()
	f.fs.files[path] = 100
	f.fs.free -= 100
	f.fs.mu.Unlock()
	return seg
}

func (f *fixture) sweeper(policy retention.Policy) *retention.Sweeper {
	return retention.New(f.idx, f.st.Segments(), policy, retention.Options{
		FS:  f.fs,
		Now: func() time.Time { return f.now },
		OnStorageLow: func(free int64) {
			f.lowMu.Lock()
			f.low = append(f.low, free)
			f.lowMu.Unlock()
		},
		ArchiveDir: "/archive",
	})
}

func TestAgeRuleDeletesOnlyCompleteSegmentsOlderThanDays(t *testing.T) {
	f := newFixture(t)
	old := f.add(t, "cam_a", 8*24*time.Hour, true)
	oldOpen := f.add(t, "cam_a", 8*24*time.Hour-time.Hour, false) // newest file, still written
	fresh := f.add(t, "cam_a", time.Hour, true)
	other := f.add(t, "cam_b", 30*24*time.Hour, true) // cam_b keeps 60 days

	s := f.sweeper(retention.Policy{DefaultDays: 7, DaysPerCamera: map[string]int{"cam_b": 60}})
	rep, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.ByAge != 1 || rep.BySpace != 0 {
		t.Fatalf("report = %+v", rep)
	}
	got := f.fs.paths()
	if len(got) != 3 || contains(got, old.Path) || !contains(got, oldOpen.Path) || !contains(got, fresh.Path) || !contains(got, other.Path) {
		t.Fatalf("files = %v", got)
	}
	segs, _ := f.idx.All(context.Background())
	if len(segs) != 3 {
		t.Fatalf("index has %d rows, want 3", len(segs))
	}
	dels, _ := f.st.Segments().Deletions(context.Background(), 10)
	if len(dels) != 1 || dels[0].Reason != "age" || dels[0].Camera != "cam_a" || dels[0].Bytes != 100 {
		t.Fatalf("deletion log = %+v", dels)
	}
}

func TestSpaceRuleDeletesOldestAcrossCamerasUntilThresholdAndReportsLow(t *testing.T) {
	f := newFixture(t)
	f.fs.free = 1000
	f.add(t, "cam_a", 3*time.Hour, true) // oldest overall
	f.add(t, "cam_b", 2*time.Hour, true)
	f.add(t, "cam_a", time.Hour, true)
	f.add(t, "cam_b", 30*time.Minute, false)
	f.add(t, "cam_a", 20*time.Minute, false)
	// free is now 500; policy wants 650 → delete two oldest (cam_a 3h, cam_b 2h).
	s := f.sweeper(retention.Policy{DefaultDays: 0, MinFreeBytes: 650})
	rep, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.BySpace != 2 || rep.ByAge != 0 {
		t.Fatalf("report = %+v", rep)
	}
	segs, _ := f.idx.All(context.Background())
	if len(segs) != 3 || segs[0].Camera != "cam_a" || !segs[0].Start.Equal(f.now.Add(-time.Hour)) {
		t.Fatalf("remaining = %+v", segs)
	}
	if len(f.low) != 0 {
		t.Fatalf("threshold reached; no storage.low expected, got %v", f.low)
	}

	// Impossible target: only incomplete segments remain deletable-ish → low.
	s2 := f.sweeper(retention.Policy{MinFreeBytes: 1 << 30})
	rep, _ = s2.Sweep(context.Background())
	if rep.BySpace != 1 { // the last complete one goes; open files never do
		t.Fatalf("second sweep = %+v", rep)
	}
	if len(f.low) != 1 {
		t.Fatalf("storage.low must fire when the target cannot be met, got %v", f.low)
	}
	segs, _ = f.idx.All(context.Background())
	for _, s := range segs {
		if s.Complete {
			t.Fatalf("a complete segment survived an unmet space target: %+v", s)
		}
	}
}

func TestMissingFileStillClearsTheIndexRow(t *testing.T) {
	f := newFixture(t)
	seg := f.add(t, "cam_a", 10*24*time.Hour, true)
	_ = f.fs.Remove(seg.Path) // someone deleted it by hand
	s := f.sweeper(retention.Policy{DefaultDays: 7})
	rep, err := s.Sweep(context.Background())
	if err != nil || rep.ByAge != 1 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
	if segs, _ := f.idx.All(context.Background()); len(segs) != 0 {
		t.Fatalf("row not cleared: %+v", segs)
	}
}

func TestSweepIsIdempotentAndZeroPolicyDeletesNothing(t *testing.T) {
	f := newFixture(t)
	f.add(t, "cam_a", 400*24*time.Hour, true)
	s := f.sweeper(retention.Policy{})
	for range 2 {
		rep, err := s.Sweep(context.Background())
		if err != nil || rep.ByAge+rep.BySpace != 0 {
			t.Fatalf("rep=%+v err=%v", rep, err)
		}
	}
	if len(f.fs.paths()) != 1 {
		t.Fatal("zero policy must keep everything")
	}
}

func TestPolicyValidation(t *testing.T) {
	for _, p := range []retention.Policy{{DefaultDays: -1}, {MinFreeBytes: -5}, {DaysPerCamera: map[string]int{"x": -2}}} {
		if err := p.Validate(); !errors.Is(err, retention.ErrInvalidPolicy) {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if err := (retention.Policy{DefaultDays: 30, MinFreeBytes: 1 << 30}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// exportsFake frees space on the fake disk when pruned.
type exportsFake struct {
	f     *fixture
	calls []time.Time
	frees int64
}

func (e *exportsFake) Prune(_ context.Context, now time.Time) (export.Pruned, error) {
	e.calls = append(e.calls, now)
	e.f.fs.mu.Lock()
	e.f.fs.free += e.frees
	e.f.fs.mu.Unlock()
	return export.Pruned{Jobs: 2, Bytes: e.frees}, nil
}

// #59: old exports go in every sweep, and before the space rule: they are
// copies, so footage is deleted for space only when removing exports was
// not enough.
func TestSweepPrunesExportsBeforeDeletingFootageForSpace(t *testing.T) {
	f := newFixture(t)
	seg := f.add(t, "cam_a", time.Hour, true)
	f.fs.free = 50 // below the target
	ex := &exportsFake{f: f, frees: 1000}
	s := retention.New(f.idx, f.st.Segments(), retention.Policy{MinFreeBytes: 500}, retention.Options{
		FS: f.fs, Now: func() time.Time { return f.now }, ArchiveDir: "/archive", Exports: ex,
	})
	rep, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.calls) != 1 || !ex.calls[0].Equal(f.now) {
		t.Fatalf("prune calls = %v", ex.calls)
	}
	if rep.Exports != 2 || rep.ExportBytes != 1000 || rep.BySpace != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := f.fs.paths(); len(got) != 1 || got[0] != seg.Path {
		t.Fatalf("footage deleted although exports freed the space: %v", got)
	}
}
