package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/update/journal"
)

type fakeChecker struct{ asked, checked int }

func (f *fakeChecker) AskNow(context.Context)   { f.asked++ }
func (f *fakeChecker) CheckNow(context.Context) { f.checked++ }

// Every 30 s the updater takes an owner's request to update now, and
// checks at once when the update settings in hive.yaml changed (#82), so
// the app sees their effect without waiting for the hourly check.
func TestWatchTakesRequestsAndChecksOnChangedSettings(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "updater")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sup := &fakeChecker{}
	w := newWatcher(dir, paths, sup, slog.New(slog.DiscardHandler))
	ctx := context.Background()

	w.tick(ctx)
	if sup.asked != 0 || sup.checked != 0 {
		t.Fatalf("nothing happened, yet %+v", sup)
	}
	if err := journal.RequestUpdate(dir); err != nil {
		t.Fatal(err)
	}
	w.tick(ctx)
	w.tick(ctx)
	if sup.asked != 1 || sup.checked != 0 {
		t.Fatalf("after one request: %+v", sup)
	}
	if err := hiveconfig.SaveUpdateSettings(paths, hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "02:00-05:00", UpdateChannel: "stable"}); err != nil {
		t.Fatal(err)
	}
	w.tick(ctx)
	w.tick(ctx)
	if sup.checked != 1 {
		t.Fatalf("after one settings change: %+v", sup)
	}
}

// run ticks until its context ends.
func TestWatchRunsUntilStopped(t *testing.T) {
	dir := t.TempDir()
	sup := &syncChecker{asked: make(chan struct{}, 1)}
	w := newWatcher(dir, hiveconfig.Paths{}.WithDataDir(t.TempDir()), sup, slog.New(slog.DiscardHandler))
	w.every = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.run(ctx); close(done) }()
	if err := journal.RequestUpdate(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sup.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the request was not taken")
	}
	cancel()
	<-done
}

type syncChecker struct{ asked chan struct{} }

func (s *syncChecker) AskNow(context.Context)   { s.asked <- struct{}{} }
func (s *syncChecker) CheckNow(context.Context) {}

// The updater runs as root every 30 s: it must never create the data
// directory or a default hive.yaml (a USB data disk that is not mounted
// yet would get a stub on the root filesystem). A missing file keeps the
// last good settings.
func TestAMissingHiveYAMLStaysMissing(t *testing.T) {
	data := filepath.Join(t.TempDir(), "usb", "camhive") // not mounted: does not exist
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(data)
	dir := t.TempDir()
	w := newWatcher(dir, paths, &fakeChecker{}, slog.New(slog.DiscardHandler))
	w.tick(context.Background())
	pol := newPolicies(paths, dir, true, slog.New(slog.DiscardHandler))
	pol.last.Mode = update.Manual
	if p := pol.current(); p.Mode != update.Manual {
		t.Fatalf("a missing hive.yaml changed the policy: %+v", p)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("the updater created %s: %v", data, err)
	}
}
