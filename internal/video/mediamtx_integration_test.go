package video_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/webrtc/v4"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

// End to end against a simulated camera: ingest, record fMP4 segments, list
// them, play a span back, and receive live video over WebRTC (WHEP), the way
// the web client will.
func TestMediaMTXEngineRecordsPlaysBackAndStreamsWebRTC(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Camera.
	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "front", Codec: camsim.H264, FPS: 10}},
		WorkDir: t.TempDir(),
	}, camsim.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(sim.Stop)
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Engine.
	archive := t.TempDir()
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: archive, SegmentDuration: 3 * time.Second, WorkDir: t.TempDir() + "/nested/work", // must be created
	}, video.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(func() { _ = eng.Stop() })
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}

	src := video.Source{Camera: "front", Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/front/main", ports[0])}
	if err := eng.AddSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	// Idempotent: adding again must not fail or duplicate.
	if err := eng.AddSource(ctx, src); err != nil {
		t.Fatalf("second AddSource: %v", err)
	}

	// Frames flow.
	waitFor(t, ctx, 20*time.Second, func() bool {
		st, err := eng.Status(ctx, "front", video.Main)
		return err == nil && st.Ready && st.BytesReceived > 0
	}, "source ready")
	st, _ := eng.Status(ctx, "front", video.Main)
	if len(st.Tracks) == 0 || !strings.Contains(st.Tracks[0], "H264") {
		t.Fatalf("tracks = %v", st.Tracks)
	}

	// Segments accumulate on disk under the archive dir.
	var segs []video.Segment
	waitFor(t, ctx, 30*time.Second, func() bool {
		segs, err = eng.Segments(ctx, "front", video.Main)
		return err == nil && len(segs) >= 2
	}, "two recorded segments")
	entries, _ := os.ReadDir(archive + "/front/main")
	if len(entries) == 0 {
		t.Fatalf("no files in %s/front/main", archive)
	}

	// Playback of the first segment returns fragmented MP4.
	url := eng.PlaybackURL("front", video.Main, segs[0].Start, 2*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatalf("playback body: %v (status %d)", err, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || string(head[4:8]) != "ftyp" {
		t.Fatalf("playback: status %d, first box %q", resp.StatusCode, head[4:8])
	}

	// Live over WebRTC: WHEP handshake, at least one RTP packet arrives.
	packets := whepReceive(t, ctx, eng.WHEPURL("front", video.Main), 5)
	if packets < 5 {
		t.Fatalf("received %d RTP packets over WebRTC, want >= 5", packets)
	}

	// Unknown source.
	if _, err := eng.Status(ctx, "ghost", video.Main); !errors.Is(err, video.ErrNotFound) {
		t.Fatalf("Status(ghost) = %v, want ErrNotFound", err)
	}
	// Remove is idempotent.
	if err := eng.RemoveSource(ctx, "front", video.Main); err != nil {
		t.Fatal(err)
	}
	if err := eng.RemoveSource(ctx, "front", video.Main); err != nil {
		t.Fatalf("second RemoveSource: %v", err)
	}
}

// ADR-0015 with H.265: the Hive pulls the camera and publishes video only
// when audio is off, live and recording work, and nobody else may publish
// into the engine's loopback RTSP server.
func TestMediaMTXEnginePullsH265WithoutAudioAndRefusesStrangers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "yard", Codec: camsim.H265, FPS: 10, Audio: true, Main: camsim.Resolution{Width: 640, Height: 360}}},
		WorkDir: t.TempDir(),
	}, camsim.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(sim.Stop)
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5], RTSPPort: ports[6],
		ArchiveDir: t.TempDir(), SegmentDuration: 3 * time.Second, WorkDir: t.TempDir(),
	}, video.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(func() { _ = eng.Stop() })
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	src := video.Source{Camera: "yard", Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/yard/main", ports[0])}
	if err := eng.AddSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ctx, 20*time.Second, func() bool {
		st, err := eng.Status(ctx, "yard", video.Main)
		return err == nil && st.Ready && st.BytesReceived > 0
	}, "H.265 source ready")
	if st, _ := eng.Status(ctx, "yard", video.Main); len(st.Tracks) != 1 || st.Tracks[0] != "H265" {
		t.Fatalf("tracks = %v, want [H265] (audio off)", st.Tracks)
	}
	if n := whepReceive(t, ctx, eng.WHEPURL("yard", video.Main), 5); n < 5 {
		t.Fatalf("received %d RTP packets over WebRTC, want >= 5", n)
	}

	// A stranger on this machine cannot publish into a camera's path.
	stranger := gortsplib.Client{}
	desc := &description.Session{Medias: []*description.Media{{Type: description.MediaTypeVideo, Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1}}}}}
	for _, u := range []string{
		fmt.Sprintf("rtsp://127.0.0.1:%d/yard/main", ports[6]),
		fmt.Sprintf("rtsp://hive:guess@127.0.0.1:%d/yard/main", ports[6]),
	} {
		if err := stranger.StartRecording(u, desc); err == nil {
			stranger.Close()
			t.Fatalf("publishing to %s was accepted", u)
		}
	}

	// A known source whose camera is gone reports not ready, not unknown.
	sim.Stop()
	waitFor(t, ctx, 20*time.Second, func() bool {
		st, err := eng.Status(ctx, "yard", video.Main)
		return err == nil && !st.Ready
	}, "not ready (without error) while the camera is gone")
}

func requireTools(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, camsim.ErrMissingTool) || errors.Is(err, video.ErrMissingTool) {
		if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
			t.Fatalf("CI requires tools: %v", err)
		}
		t.Skipf("skipping: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ctx context.Context, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// whepReceive performs a WHEP handshake as a browser would and counts RTP
// packets on the first video track.
func whepReceive(t *testing.T, ctx context.Context, whepURL string, n int) int {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{}, n)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			select {
			case got <- struct{}{}:
			default:
			}
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, whepURL, strings.NewReader(pc.LocalDescription().SDP))
	req.Header.Set("Content-Type", "application/sdp")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("WHEP: status %d: %s", resp.StatusCode, body)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(body)}); err != nil {
		t.Fatal(err)
	}

	count := 0
	deadline := time.After(20 * time.Second)
	for count < n {
		select {
		case <-got:
			count++
		case <-deadline:
			return count
		case <-ctx.Done():
			return count
		}
	}
	return count
}
