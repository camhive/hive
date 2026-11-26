package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/recording"
	"github.com/camhive/hive/internal/version"
)

// While the data directory's disk is missing (identityGuard), the hive
// waits for it instead of exiting (#172 review): hive-updater reads an
// exit as a failed update and rolls back, or restarts the hive every 5 s.
// It answers /healthz on its loopback address with its version and
// "not recording", checks every 30 s, and starts for real once the disk
// is mounted. The cloud cannot be told: its link lives on that disk too;
// it reports the hive offline after five minutes.

// diskWaitEvery is how often a waiting hive checks for its disk.
const diskWaitEvery = 30 * time.Second

// waitingHandler is a waiting hive's API: /healthz only.
func waitingHandler(version string, since time.Time, detail string) http.Handler {
	zero := 0
	health := hiveapp.Health{Status: "ok", Version: version, Recording: &zero,
		NotRecording: &hiveapp.NotRecording{Since: since.UTC(), Reason: recording.DiskNotMounted, Detail: detail}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(health)
	})
	return mux
}

// serveWaiting serves h on addr until it is shut down; nil when the
// address is taken (the health check then fails, as before).
func serveWaiting(ctx context.Context, addr string, h http.Handler, log *slog.Logger) *http.Server {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		log.Error("waiting for the disk: cannot answer /healthz", "addr", addr, "err", err)
		return nil
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("waiting for the disk: /healthz", "err", err)
		}
	}()
	return srv
}

// waitFor checks at every tick until check passes (true) or ctx ends
// (false), with a warning every ten checks.
func waitFor(ctx context.Context, check func() error, tick <-chan time.Time, log *slog.Logger) bool {
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return false
		case <-tick:
		}
		err := check()
		if err == nil {
			return true
		}
		if n%10 == 0 {
			log.Warn("still waiting for the data directory's disk", "err", err)
		}
	}
}

// waitForDisk serves the waiting /healthz until the guard passes, then
// starts the hive; it ends the service's run when ctx ends first.
func (p *program) waitForDisk(ctx context.Context, cause error) {
	since := time.Now()
	srv := serveWaiting(ctx, hiveConfig(p.paths, io.Discard).ListenAddr, waitingHandler(version.Current().String(), since, cause.Error()), p.log)
	t := time.NewTicker(diskWaitEvery)
	back := waitFor(ctx, func() error { return identityGuard(os.Getenv("CAMHIVE_DATA_DIR"), systemdUnit, otherFS) }, t.C, p.log)
	t.Stop()
	if srv != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(sctx)
		cancel()
	}
	if !back {
		close(p.done)
		return
	}
	p.log.Info("the data directory's disk is back; starting")
	if err := p.start(ctx); err != nil {
		p.log.Error("start after the disk came back", "err", err)
		close(p.done)
		os.Exit(1) //nolint:revive // as a failed start before: the service manager restarts the hive
	}
}
