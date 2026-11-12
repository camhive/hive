package retention_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/retention"
)

// put indexes a 10-minute, 100-byte segment of cam/stream starting `age` ago.
func (f *fixture) put(t *testing.T, cam, stream string, age time.Duration, complete bool) archive.Segment {
	t.Helper()
	start := f.now.Add(-age)
	path := filepath.Join("/archive", cam, stream, start.Format("2006-01-02_15-04-05")+"-000000_Z.mp4")
	seg := archive.Segment{Camera: cam, Stream: stream, Start: start, Duration: 10 * time.Minute, Bytes: 100, Path: path, Complete: complete}
	if err := f.idx.Upsert(context.Background(), seg); err != nil {
		t.Fatal(err)
	}
	f.fs.mu.Lock()
	f.fs.files[path] = 100
	f.fs.mu.Unlock()
	return seg
}

func (f *fixture) has(path string) bool {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	_, ok := f.fs.files[path]
	return ok
}

// ADR-0016: whole segments of both streams that touch the range go, so the
// deleted span is wider than asked, never narrower; a dry run only reports.
func TestOwnerDeletionRoundsOutwardsOverBothStreamsOfTheNamedCamera(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	keep, gone := make([]archive.Segment, 0, 3), make([]archive.Segment, 0, 4)
	for _, stream := range []string{"main", "sub"} {
		keep = append(keep, f.put(t, "cam_a", stream, 3*time.Hour, true))
		gone = append(gone, f.put(t, "cam_a", stream, 170*time.Minute, true), f.put(t, "cam_a", stream, 160*time.Minute, true))
	}
	keep = append(keep, f.put(t, "cam_b", "main", 170*time.Minute, true))
	s := f.sweeper(retention.Policy{})
	scope := retention.Scope{Cameras: []string{"cam_a"}, From: f.now.Add(-165 * time.Minute), To: f.now.Add(-155 * time.Minute)}

	plan, err := s.Delete(ctx, scope, true)
	if err != nil {
		t.Fatal(err)
	}
	want := retention.CameraPlan{Camera: "cam_a", From: f.now.Add(-170 * time.Minute), To: f.now.Add(-150 * time.Minute), Segments: 4, Bytes: 400}
	if len(plan.Cameras) != 1 || plan.Cameras[0] != want {
		t.Fatalf("plan = %+v, want %+v", plan.Cameras, want)
	}
	for _, seg := range gone {
		if !f.has(seg.Path) {
			t.Fatal("dry run deleted a file")
		}
	}

	done, err := s.Delete(ctx, scope, false)
	if err != nil || len(done.Cameras) != 1 || done.Cameras[0] != want {
		t.Fatalf("delete = %+v, %v", done, err)
	}
	for _, seg := range gone {
		if f.has(seg.Path) {
			t.Errorf("%s still on disk", seg.Path)
		}
	}
	for _, seg := range keep {
		if !f.has(seg.Path) {
			t.Errorf("%s deleted", seg.Path)
		}
	}
	if rest, _ := f.idx.All(ctx); len(rest) != len(keep) {
		t.Fatalf("index keeps %d segments, want %d", len(rest), len(keep))
	}
	log, _ := f.st.Segments().Deletions(ctx, 10)
	if len(log) != 4 || log[0].Reason != "owner" {
		t.Fatalf("deletion log = %+v", log)
	}
}

// The file still being written waits; it goes once it is closed, even after
// a restart. A newest file nobody writes any more (camera off) goes at once.
func TestOwnerDeletionWaitsForTheFileBeingWrittenAndTakesIdleOnesNow(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old := f.put(t, "cam_a", "main", 20*time.Minute, true)
	writing := f.put(t, "cam_a", "main", 10*time.Minute, false)
	f.fs.mod[writing.Path] = f.now.Add(-time.Second)
	idle := f.put(t, "cam_off", "main", 5*24*time.Hour, false) // last write long ago

	plan, err := f.sweeper(retention.Policy{}).Delete(ctx, retention.Scope{To: f.now}, false) // everything, every camera
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cameras) != 2 || plan.Cameras[0].Camera != "cam_a" || plan.Cameras[0].Segments != 2 || plan.Cameras[0].Pending != 1 || plan.Cameras[1].Pending != 0 {
		t.Fatalf("plan = %+v", plan.Cameras)
	}
	if f.has(old.Path) || f.has(idle.Path) || !f.has(writing.Path) {
		t.Fatalf("after delete: old %v idle %v writing %v", f.has(old.Path), f.has(idle.Path), f.has(writing.Path))
	}

	// The engine closes the file and starts the next one, after the range.
	writing.Complete = true
	if err := f.idx.Upsert(ctx, writing); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	next := f.put(t, "cam_a", "main", 0, false)
	f.fs.mod[next.Path] = f.now

	restarted := f.sweeper(retention.Policy{})
	rep, err := restarted.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ByOwner != 1 || f.has(writing.Path) || !f.has(next.Path) {
		t.Fatalf("sweep = %+v, writing kept %v, next kept %v", rep, f.has(writing.Path), f.has(next.Path))
	}
	if p, _ := f.st.Segments().Pending(ctx); len(p) != 0 {
		t.Fatalf("pending left: %+v", p)
	}
}

// Exports of the footage go first when asked; a running one stops everything.
func TestOwnerDeletionTakesExportsAlongAndStopsForARunningOne(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	seg := f.put(t, "cam_a", "main", time.Hour, true)
	ex := &exportsFake{f: f}
	ex.match = func(m export.Footage) []bool {
		at := f.now.Add(-time.Hour)
		return []bool{m("cam_a", at, at.Add(time.Minute)), m("cam_b", at, at.Add(time.Minute)), m("cam_a", f.now.Add(time.Hour), f.now.Add(2*time.Hour))}
	}
	s := retention.New(f.idx, f.st.Segments(), retention.Policy{}, retention.Options{FS: f.fs, Now: func() time.Time { return f.now }, Exports: ex})
	scope := retention.Scope{Cameras: []string{"cam_a"}, To: f.now, Exports: true}

	ex.removeErr = export.ErrRunning
	if _, err := s.Delete(ctx, scope, false); !errors.Is(err, export.ErrRunning) || !f.has(seg.Path) {
		t.Fatalf("err = %v, footage kept %v", err, f.has(seg.Path))
	}
	ex.removeErr = nil
	plan, err := s.Delete(ctx, scope, false)
	if err != nil || plan.Exports.Jobs != 1 || f.has(seg.Path) {
		t.Fatalf("plan = %+v, err %v", plan, err)
	}
	if want := []bool{true, false, false}; len(ex.matched) != 3 || ex.matched[0] != want[0] || ex.matched[1] != want[1] || ex.matched[2] != want[2] {
		t.Fatalf("matched = %v, want %v", ex.matched, want)
	}
	// Without the flag exports are left alone.
	n := len(ex.removes)
	if _, err := s.Delete(ctx, retention.Scope{To: f.now}, false); err != nil || len(ex.removes) != n {
		t.Fatalf("exports touched without the flag: %v", err)
	}
}

func TestOwnerDeletionRejectsAnEmptyOrBackwardsRange(t *testing.T) {
	f := newFixture(t)
	s := f.sweeper(retention.Policy{})
	for _, sc := range []retention.Scope{{}, {From: f.now, To: f.now}, {From: f.now, To: f.now.Add(-time.Minute)}} {
		if _, err := s.Delete(context.Background(), sc, true); !errors.Is(err, retention.ErrInvalidScope) {
			t.Errorf("%+v: err = %v", sc, err)
		}
	}
}

// Run sweeps at once, which also finishes waiting owner deletions, and
// stops with its context.
func TestRunSweepsAtOnceAndStopsWithItsContext(t *testing.T) {
	f := newFixture(t)
	old := f.put(t, "cam_a", "main", 40*24*time.Hour, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.sweeper(retention.Policy{DefaultDays: 30}).Run(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.has(old.Path) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if f.has(old.Path) {
		t.Fatal("Run did not sweep")
	}
}
