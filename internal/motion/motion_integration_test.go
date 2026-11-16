package motion_test

import (
	"context"
	"encoding/json"
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
	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}

// ADR-0020 end to end: a simulated camera shows a still scene that an object
// crosses for 8 s every 24 s. The Hive pulls it through the real engine,
// reads the sub stream's frame sizes, and records one motion Event with a
// start and an end, sourced from frames.
func TestACrossingObjectOnCamsimBecomesAMotionEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	const period = 24 * time.Second
	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "gate", Codec: camsim.H264, FPS: 15, Motion: period}},
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
	started := time.Now()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := motion.Defaults()
	cfg.Hold = 3 * time.Second
	ended := make(chan store.Event, 4)
	hub := motion.NewHub(st.Events(), motion.Options{Config: cfg, Logger: log, Observe: func(e store.Event) {
		if !e.End.IsZero() {
			ended <- e
		}
	}})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go hub.Run(runCtx)

	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], WebRTCPort: ports[4],
		ArchiveDir: t.TempDir(), SegmentDuration: time.Minute, WorkDir: t.TempDir(),
	}, video.WithLogger(log), video.WithFrames(hub.Frame))
	if errors.Is(err, video.ErrMissingTool) {
		skipOrFail(t, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	url := func(s string) string { return fmt.Sprintf("rtsp://127.0.0.1:%d/gate/%s", ports[0], s) }
	cam, _ := camera.New(camera.Spec{
		Name: "Gate", Source: camera.Source{Kind: camera.SourceRTSP, Address: url("main")},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: url("main")}, camera.Sub: {URL: url("sub")}},
	}, time.Now())
	if err := st.Cameras().Put(ctx, cam); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = ingest.New(st.Cameras(), eng, ingest.Options{Interval: time.Hour, Logger: log}).Run(runCtx)
	}()

	var e store.Event
	select {
	case e = <-ended:
	case <-time.After(70 * time.Second):
		t.Fatal("no motion event ended within 70 s")
	}
	moving := started.Add(period * 2 / 3) // the object enters 16 s into each period
	if e.Camera != string(cam.ID) || e.Kind != motion.Kind {
		t.Fatalf("event %+v", e)
	}
	if lead := e.At.Sub(moving); lead < -3*time.Second || lead > 5*time.Second {
		t.Fatalf("motion began %v after the object entered (start %v)", lead, e.At)
	}
	if d := e.End.Sub(e.At); d < 4*time.Second || d > 14*time.Second {
		t.Fatalf("motion lasted %v, the object crosses for 8 s", d)
	}
	var data struct{ Source string }
	rows, _ := st.Events().Range(ctx, string(cam.ID), started, time.Now())
	if len(rows) != 1 || json.Unmarshal(rows[0].Data, &data) != nil || data.Source != string(motion.SourceFrames) {
		t.Fatalf("want one stored motion event from frames, got %+v", rows)
	}
	t.Logf("motion %v → %v (%v); dropped frames %d", e.At.Sub(started), e.End.Sub(started), e.End.Sub(e.At), hub.Dropped())
}
