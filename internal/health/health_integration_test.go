package health_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// A camera that stops publishing is reported offline within 15 seconds and
// online again when it returns.
func TestOfflineWithin15sAndOnlineOnReturn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	simCfg := camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "hc", Codec: camsim.H264, FPS: 10, Main: camsim.Resolution{Width: 320, Height: 180}}},
	}
	startSim := func() *camsim.Simulator {
		cfg := simCfg
		cfg.WorkDir = t.TempDir()
		sim, err := camsim.New(cfg, camsim.WithLogger(log))
		if errors.Is(err, camsim.ErrMissingTool) {
			skipOrFail(t, err)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := sim.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return sim
	}
	sim := startSim()

	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: t.TempDir(), SegmentDuration: time.Minute, WorkDir: t.TempDir(),
	}, video.WithLogger(log))
	if errors.Is(err, video.ErrMissingTool) {
		skipOrFail(t, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Stop() })
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cam, _ := camera.New(camera.Spec{Name: "HC", Source: camera.Source{Kind: camera.SourceRTSP, Address: fmt.Sprintf("rtsp://127.0.0.1:%d/hc/main", ports[0])},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: fmt.Sprintf("rtsp://127.0.0.1:%d/hc/main", ports[0])}}}, time.Now())
	if err := eng.AddSource(ctx, video.Source{Camera: video.CameraID(cam.ID), Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/hc/main", ports[0])}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var kinds []string
	m := health.New(&memStore{cams: []camera.Camera{cam}}, eng, health.Options{Interval: time.Second, OfflineAfter: 2, Logger: log,
		OnEvent: func(e health.Event) {
			mu.Lock()
			kinds = append(kinds, e.Kind)
			mu.Unlock()
		}})
	go m.Run(ctx)
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(kinds) == 0 {
			return ""
		}
		return kinds[len(kinds)-1]
	}
	wait := func(want string, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			if last() == want {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("expected %s within %v, last event %q", want, within, last())
	}
	wait("camera.online", 20*time.Second)

	sim.Stop() // the camera goes away
	wait("camera.offline", 15*time.Second)

	// Return: the engine re-pulls the source on its own retry schedule
	// (several seconds), so the bound here is generous; only the offline
	// bound above is a product promise.
	sim2 := startSim()
	t.Cleanup(sim2.Stop)
	wait("camera.online", 90*time.Second)
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}
