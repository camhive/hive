package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/ingest"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// ADR-0015 end to end: a camera with a microphone, the Hive pulling it and
// publishing to a real engine. With record_audio off the engine sees and
// records video only; switching it on brings sound into new segments; a
// camera that goes away and comes back is pulled again.
func TestIngestRecordsAudioOnlyWhenTheCameraAllowsItAndReconnects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	simCfg := camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "hall", Codec: camsim.H264, FPS: 10, Audio: true, Main: camsim.Resolution{Width: 640, Height: 360}}},
	}
	sim := startSim(ctx, t, simCfg, log)

	archiveDir := t.TempDir()
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: archiveDir, SegmentDuration: 2 * time.Second, WorkDir: t.TempDir(),
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
	url := fmt.Sprintf("rtsp://127.0.0.1:%d/hall/main", ports[0])
	cam, _ := camera.New(camera.Spec{
		Name: "Hall", Source: camera.Source{Kind: camera.SourceRTSP, Address: url},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: url}},
	}, time.Now())
	if err := st.Cameras().Put(ctx, cam); err != nil {
		t.Fatal(err)
	}

	ing := ingest.New(st.Cameras(), eng, ingest.Options{Interval: time.Hour, Logger: log})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = ing.Run(runCtx) }()

	camID := video.CameraID(cam.ID)
	tracks := func() []string {
		s, err := eng.Status(ctx, camID, video.Main)
		if err != nil || !s.Ready {
			return nil
		}
		return s.Tracks
	}
	segDir := filepath.Join(archiveDir, string(cam.ID), "main")

	// Off (the default): video only, live and on disk.
	waitFor(t, 20*time.Second, func() bool { return len(tracks()) > 0 }, "camera published with audio off")
	if got := tracks(); !slices.Equal(got, []string{"H264"}) {
		t.Fatalf("engine tracks with audio off = %v, want [H264]", got)
	}
	off := waitCompleteSegment(t, archiveDir, segDir, time.Time{})
	if _, err := archive.AudioWAV(bytes.NewReader(readFile(t, off))); !errors.Is(err, archive.ErrNoAudio) {
		t.Fatalf("segment recorded with audio off: AudioWAV err = %v, want ErrNoAudio", err)
	}

	// On: the pull restarts with the audio media; new segments carry sound.
	switched := time.Now()
	on := true
	withAudio, _ := cam.Update(camera.Patch{RecordAudio: &on}, time.Now())
	if err := st.Cameras().Put(ctx, withAudio); err != nil {
		t.Fatal(err)
	}
	ing.Notify()
	waitFor(t, 20*time.Second, func() bool { return len(tracks()) == 2 }, "audio track published after switching on")
	onSeg := waitCompleteSegment(t, archiveDir, segDir, switched)
	wav, err := archive.AudioWAV(bytes.NewReader(readFile(t, onSeg)))
	if err != nil || len(wav) <= 44 {
		t.Fatalf("segment recorded with audio on: %d bytes of WAV, err %v", len(wav), err)
	}

	// The camera goes away: the engine path stops being ready; it comes back
	// on the same address and the pull reconnects by itself.
	sim.Stop()
	waitFor(t, 20*time.Second, func() bool { return len(tracks()) == 0 }, "stream not ready while the camera is gone")
	startSim(ctx, t, simCfg, log)
	waitFor(t, 45*time.Second, func() bool { return len(tracks()) == 2 }, "pull reconnected after the camera came back")
}

func startSim(ctx context.Context, t *testing.T, cfg camsim.Config, log *slog.Logger) *camsim.Simulator {
	t.Helper()
	cfg.WorkDir = t.TempDir()
	sim, err := camsim.New(cfg, camsim.WithLogger(log))
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
	return sim
}

// waitCompleteSegment returns the oldest segment file that started at or
// after since and has a newer segment after it (so it is finished).
func waitCompleteSegment(t *testing.T, root, dir string, since time.Time) string {
	t.Helper()
	var found string
	waitFor(t, 30*time.Second, func() bool {
		entries, _ := os.ReadDir(dir)
		var segs []archive.Segment
		for _, e := range entries {
			if seg, ok := archive.ParsePath(root, filepath.Join(dir, e.Name())); ok {
				segs = append(segs, seg)
			}
		}
		sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Before(segs[j].Start) })
		for i, seg := range segs[:max(len(segs)-1, 0)] {
			if !seg.Start.Before(since) && i < len(segs)-1 {
				found = seg.Path
				return true
			}
		}
		return false
	}, "a finished segment in "+dir)
	return found
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test archive
	if err != nil {
		t.Fatal(err)
	}
	return b
}
