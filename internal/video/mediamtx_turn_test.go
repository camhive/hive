package video_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v4"
	"github.com/pion/webrtc/v4"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

const (
	turnUser = "hive"
	turnPass = "relay-secret"
)

// startTURN runs an in-process TURN server on 127.0.0.1 (pion/turn, the same
// protocol coturn speaks) and returns its address.
func startTURN(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key := turn.GenerateAuthKey(turnUser, "camhive", turnPass)
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: "camhive",
		AuthHandler: func(username, _ string, _ net.Addr) ([]byte, bool) {
			if username == turnUser {
				return key, true
			}
			return nil, false
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: pc,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP("127.0.0.1"),
				Address:      "127.0.0.1",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return pc.LocalAddr().String()
}

// A viewer that is only allowed relay candidates (ICE policy "relay") must
// still receive live video: this is the "P2P blocked" path.
func TestWebRTCFlowsThroughTURNWhenP2PIsBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	turnAddr := startTURN(t)

	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "gate", Codec: camsim.H264, FPS: 10, Main: camsim.Resolution{Width: 640, Height: 360}}},
		WorkDir: t.TempDir(),
	}, camsim.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(sim.Stop)
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}

	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: ports[3], PlaybackPort: ports[4], WebRTCPort: ports[5],
		ArchiveDir: t.TempDir(), SegmentDuration: time.Minute, WorkDir: t.TempDir(),
		ICEServers: []video.ICEServer{{URL: "turn:" + turnAddr, Username: turnUser, Password: turnPass}},
	}, video.WithLogger(log))
	requireTools(t, err)
	t.Cleanup(func() { _ = eng.Stop() })
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.AddSource(ctx, video.Source{Camera: "gate", Stream: video.Main, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/gate/main", ports[0])}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ctx, 20*time.Second, func() bool {
		st, err := eng.Status(ctx, "gate", video.Main)
		return err == nil && st.Ready
	}, "source ready")

	packets, pairType := whepReceiveRelayOnly(t, ctx, eng.WHEPURL("gate", video.Main), turnAddr, 5)
	if packets < 5 {
		t.Fatalf("received %d RTP packets through TURN, want >= 5", packets)
	}
	if pairType != "relay" {
		t.Fatalf("selected local candidate type = %q, want relay", pairType)
	}
}

// whepReceiveRelayOnly is whepReceive with ICE restricted to TURN relay
// candidates; it also returns the type of the local candidate actually used.
func whepReceiveRelayOnly(t *testing.T, ctx context.Context, whepURL, turnAddr string, n int) (int, string) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
		ICEServers: []webrtc.ICEServer{{
			URLs: []string{"turn:" + turnAddr + "?transport=udp"}, Username: turnUser, Credential: turnPass,
		}},
	})
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
	sdp := pc.LocalDescription().SDP
	if strings.Contains(sdp, " typ host") || strings.Contains(sdp, " typ srflx") {
		t.Fatalf("relay policy leaked non-relay candidates:\n%s", sdp)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, whepURL, strings.NewReader(sdp))
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
	deadline := time.After(30 * time.Second)
	for count < n {
		select {
		case <-got:
			count++
		case <-deadline:
			return count, selectedLocalType(pc)
		case <-ctx.Done():
			return count, selectedLocalType(pc)
		}
	}
	return count, selectedLocalType(pc)
}

func selectedLocalType(pc *webrtc.PeerConnection) string {
	for _, tr := range pc.GetTransceivers() {
		if tr.Receiver() == nil || tr.Receiver().Transport() == nil {
			continue
		}
		pair, err := tr.Receiver().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || pair == nil || pair.Local == nil {
			continue
		}
		return fmt.Sprint(pair.Local.Typ)
	}
	return ""
}
