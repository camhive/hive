package export_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// A real export: recorded by MediaMTX from camsim with sound, read by the
// hive's archive reader across a segment boundary, and accepted by ffprobe
// with the expected duration and both tracks; a span further in (a seek)
// plays as video only.
func TestExportFromRealEngineIsAValidMP4(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "exp", Codec: camsim.H264, FPS: 10, Audio: true, Main: camsim.Resolution{Width: 320, Height: 180}}},
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
	archiveDir := t.TempDir()
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], WebRTCPort: ports[5],
		ArchiveDir: archiveDir, SegmentDuration: 3 * time.Second, WorkDir: t.TempDir(),
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
	if err := eng.AddSource(ctx, video.Source{Camera: "cam_exp", Stream: video.Main, Audio: true, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/exp/main", ports[0])}); err != nil {
		t.Fatal(err)
	}
	var segs []video.Segment
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		segs, _ = eng.Segments(ctx, "cam_exp", video.Main)
		if len(segs) >= 3 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(segs) < 3 {
		t.Fatalf("only %d segments", len(segs))
	}

	// Span from 1s into the first segment for 5s: crosses into the second.
	fetch := hiveapp.ArchiveFetch(archive.NewReader(archiveDir, archive.OpenPlain))
	m := export.NewManager(t.TempDir(), fetch, jobs(t))
	job, err := m.Start(ctx, export.Request{Camera: "cam_exp", Stream: "main", Start: segs[0].Start.Add(time.Second), Duration: 5 * time.Second, Owner: "acc"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitStatus(t, m, job.ID, export.StatusDone)
	path, _ := m.FilePath(ctx, final.ID)
	data, _ := os.ReadFile(path)
	if len(data) < 1000 || string(data[4:8]) != "ftyp" {
		t.Fatalf("export file: %d bytes, box %q", len(data), data[4:8])
	}

	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Log("ffprobe not installed; skipping container validation")
		return
	}
	if d, codecs := probe(ctx, t, ffprobe, path); d < 4 || d > 6.5 || codecs != "h264,pcm_s16be" {
		t.Fatalf("export: %.2fs %s, want ~5s of h264 and pcm_s16be", d, codecs)
	}

	// A seek: 2 s from 1 s into the second segment, as the player asks.
	body, err := fetch(ctx, export.Request{Camera: "cam_exp", Stream: "main", Start: segs[1].Start.Add(time.Second), Duration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	seek := filepath.Join(t.TempDir(), "seek.mp4")
	var out bytes.Buffer
	if err := archive.CopyPlayable(&out, body); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(seek, out.Bytes(), 0o600)
	if d, codecs := probe(ctx, t, ffprobe, seek); d < 1.5 || d > 4 || codecs != "h264" {
		t.Fatalf("seek: %.2fs %s, want ~2s of h264 only", d, codecs)
	}
}

// probe returns a file's duration and codecs as ffprobe sees them.
func probe(ctx context.Context, t *testing.T, ffprobe, path string) (float64, string) {
	t.Helper()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration:stream=codec_name", "-of", "csv=p=0", path).Output() //nolint:gosec // test tool with fixed args
	if err != nil {
		t.Fatalf("ffprobe rejected %s: %v", path, err)
	}
	var codecs []string
	var d float64
	for _, line := range strings.Fields(strings.TrimSpace(string(out))) {
		if v, err := strconv.ParseFloat(line, 64); err == nil {
			d = v
		} else {
			codecs = append(codecs, line)
		}
	}
	return d, strings.Join(codecs, ",")
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}
