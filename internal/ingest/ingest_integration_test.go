package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/ingest"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// Real store, real MediaMTX, simulated camera: enabling a camera starts
// recording within seconds, disabling stops it, and a killed MediaMTX comes
// back with its sources without operator action.
func TestIngestWithRealEngineRecordsRecoversAndStops(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Username: "admin", Password: "pw",
		Cameras: []camsim.Camera{{Name: "door", Codec: camsim.H264, FPS: 10, Main: camsim.Resolution{Width: 640, Height: 360}}},
		WorkDir: t.TempDir(),
	}, camsim.WithLogger(log))
	if errors.Is(err, camsim.ErrMissingTool) {
		skipOrFail(t, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Stop)
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}

	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: t.TempDir(), SegmentDuration: 5 * time.Second, WorkDir: t.TempDir(),
	}, video.WithLogger(log))
	if errors.Is(err, video.ErrMissingTool) {
		skipOrFail(t, err)
	}
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cam, _ := camera.New(camera.Spec{
		Name:   "Door",
		Source: camera.Source{Kind: camera.SourceRTSP, Address: fmt.Sprintf("rtsp://127.0.0.1:%d/door/main", ports[0])},
		Streams: map[camera.Stream]camera.StreamSpec{
			camera.Main: {URL: fmt.Sprintf("rtsp://127.0.0.1:%d/door/main", ports[0])},
			camera.Sub:  {URL: fmt.Sprintf("rtsp://127.0.0.1:%d/door/sub", ports[0])},
		},
	}, time.Now())
	if err := st.Cameras().Put(ctx, cam); err != nil {
		t.Fatal(err)
	}

	ing := ingest.New(st.Cameras(), eng, ingest.Options{Interval: time.Hour, RestartBackoff: 200 * time.Millisecond, Logger: log})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = ing.Run(runCtx) }()

	camID, main := video.CameraID(cam.ID), video.Main
	ready := func() bool {
		s, err := eng.Status(ctx, camID, main)
		return err == nil && s.Ready
	}
	waitFor(t, 10*time.Second, ready, "recording within 10s of enable")

	// Kill the engine process: ingest must restart it and re-add sources.
	pid := eng.PID()
	if pid == 0 {
		t.Fatal("no engine pid")
	}
	proc, _ := os.FindProcess(pid)
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool { return eng.PID() != 0 && eng.PID() != pid && ready() }, "engine restarted with sources")

	// Disable the camera and poke the loop: the source disappears.
	e := false
	off, _ := cam.Update(camera.Patch{Enabled: &e}, time.Now())
	if err := st.Cameras().Put(ctx, off); err != nil {
		t.Fatal(err)
	}
	ing.Notify()
	waitFor(t, 5*time.Second, func() bool {
		_, err := eng.Status(ctx, camID, main)
		return errors.Is(err, video.ErrNotFound)
	}, "source removed within 5s of disable")
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
