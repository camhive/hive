package retention_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// Recording never stops because of retention: with an impossible free-space
// target the sweeper deletes every complete segment it finds, yet the open
// file is never touched, the engine keeps recording and new segments keep
// arriving, and every deletion is logged.
func TestSweeperUnderPressureNeverStopsRecording(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "ret", Codec: camsim.H264, FPS: 10, Main: camsim.Resolution{Width: 320, Height: 180}}},
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
	root := t.TempDir()
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: root, SegmentDuration: 2 * time.Second, WorkDir: t.TempDir(),
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
	if err := eng.AddSource(ctx, video.Source{Camera: "cam_ret", Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/ret/main", ports[0])}); err != nil {
		t.Fatal(err)
	}

	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	defer st.Close()
	idx := archive.NewIndex(st)
	lowCalls := 0
	sw := retention.New(idx, st.Segments(), retention.Policy{MinFreeBytes: 1 << 62}, retention.Options{
		ArchiveDir: root, OnStorageLow: func(int64) { lowCalls++ }, Logger: log,
	})

	deleted := 0
	deadline := time.Now().Add(14 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := idx.Reconcile(ctx, root); err != nil {
			t.Fatal(err)
		}
		rep, err := sw.Sweep(ctx)
		if err != nil {
			t.Fatal(err)
		}
		deleted += rep.BySpace
		st, err := eng.Status(ctx, "cam_ret", video.Main)
		if err != nil || !st.Ready {
			t.Fatalf("recording stopped under retention pressure: %+v %v", st, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if deleted < 3 {
		t.Fatalf("expected several deletions under pressure, got %d", deleted)
	}
	if lowCalls == 0 {
		t.Fatal("storage.low must have been raised")
	}
	// The newest file (still being written) is on disk; the engine still records.
	segs, _ := idx.All(ctx)
	if len(segs) == 0 {
		t.Fatal("index empty; the open segment must remain")
	}
	for _, s := range segs {
		if s.Complete {
			t.Fatalf("a complete segment survived: %+v", s)
		}
		if _, err := os.Stat(s.Path); err != nil {
			t.Fatalf("open segment file missing: %v", err)
		}
	}
	dels, _ := st.Segments().Deletions(ctx, 100)
	if len(dels) != deleted {
		t.Fatalf("deletion log has %d rows, %d deletions made", len(dels), deleted)
	}
	engSegs, _ := eng.Segments(ctx, "cam_ret", video.Main)
	if len(engSegs) == 0 {
		t.Fatal("engine reports no recordings; recording must continue")
	}
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}
