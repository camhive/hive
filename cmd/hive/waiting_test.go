package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/version"
)

// #172 review: a hive whose data directory's disk is missing does not
// exit (hive-updater would read that as a failed update and roll back, or
// restart it every 5 s); it answers /healthz with its version, so the
// updater's health check passes, and says it does not record.
func TestAHiveWaitingForItsDiskIsHealthyForTheUpdater(t *testing.T) {
	since := time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(waitingHandler("0.1.11 (abc, 2026-11-25)", since, "/mnt/cameras/camhive has no hive.key"))
	defer srv.Close()
	h, err := update.HTTPHealth(srv.URL, nil)(context.Background())
	want, _ := update.ParseVersion("0.1.11")
	if err != nil || h.Version.Compare(want) != 0 || h.Recording != 0 {
		t.Fatalf("health = %+v, %v; want healthy 0.1.11 recording nothing", h, err)
	}

	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if err := os.WriteFile(paths.Config, []byte("listen_addr: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	reportHive(context.Background(), &out, io.Discard, version.Info{Version: "0.1.11"}, paths)
	if !strings.Contains(out.String(), "NOT RECORDING since") || !strings.Contains(out.String(), "disk_not_mounted") {
		t.Fatalf("hive status =\n%s\nwant NOT RECORDING … disk_not_mounted", out.String())
	}
}

// The wait ends when the guard passes (the disk is mounted again), or
// when the service stops.
func TestWaitingEndsWhenTheDiskIsBackOrTheServiceStops(t *testing.T) {
	ticks := make(chan time.Time)
	var missing atomic.Bool
	missing.Store(true)
	check := func() error {
		if missing.Load() {
			return errDiskMissing
		}
		return nil
	}
	done := make(chan bool)
	go func() { done <- waitFor(context.Background(), check, ticks, discard()) }()
	ticks <- time.Now()
	missing.Store(false)
	ticks <- time.Now()
	if !<-done {
		t.Fatal("the wait did not end with the disk back")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- waitFor(ctx, func() error { return errors.New("still missing") }, ticks, discard()) }()
	cancel()
	if <-done {
		t.Fatal("the wait reported the disk back after the service stopped")
	}
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
