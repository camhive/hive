package archive_test

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
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// Real segments from MediaMTX: our fMP4 duration parser agrees with the
// engine's own recordings API and the index matches the files.
func TestReconcileAgainstRealRecordings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "idx", Codec: camsim.H264, FPS: 10, Main: camsim.Resolution{Width: 320, Height: 180}}},
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
		ArchiveDir: root, SegmentDuration: 3 * time.Second, WorkDir: t.TempDir(),
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
	if err := eng.AddSource(ctx, video.Source{Camera: "cam_idx", Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/idx/main", ports[0])}); err != nil {
		t.Fatal(err)
	}

	// Wait for three segments according to the engine.
	var engSegs []video.Segment
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		engSegs, _ = eng.Segments(ctx, "cam_idx", video.Main)
		if len(engSegs) >= 3 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(engSegs) < 3 {
		t.Fatalf("engine reports %d segments", len(engSegs))
	}

	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	defer st.Close()
	idx := archive.NewIndex(st, archive.WithLogger(log))
	rep, err := idx.Reconcile(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added < 3 {
		t.Fatalf("reconcile = %+v", rep)
	}
	ours, _ := idx.Range(ctx, "cam_idx", "main", time.Time{}, time.Now().Add(time.Hour))
	if len(ours) < 3 {
		t.Fatalf("index has %d segments", len(ours))
	}
	// Completed segments: our parsed duration agrees with MediaMTX within 0.5s.
	checked := 0
	for _, o := range ours {
		if !o.Complete {
			continue
		}
		for _, e := range engSegs {
			if e.Start.Equal(o.Start) && e.Duration > 0 {
				if diff := (o.Duration - e.Duration).Abs(); diff > 500*time.Millisecond {
					t.Errorf("segment %s: parsed %v, engine %v", o.Start.Format(time.RFC3339), o.Duration, e.Duration)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		for _, o := range ours {
			t.Logf("index : %s %v complete=%v", o.Start.Format(time.RFC3339Nano), o.Duration, o.Complete)
		}
		for _, e := range engSegs {
			t.Logf("engine: %s %v", e.Start.Format(time.RFC3339Nano), e.Duration)
		}
		t.Fatal("no completed segment compared against the engine")
	}
	days, _ := idx.Calendar(ctx, "cam_idx", "main", time.UTC)
	if len(days) == 0 {
		t.Fatal("calendar empty")
	}
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}
