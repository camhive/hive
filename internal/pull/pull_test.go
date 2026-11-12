package pull

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/camhive/hive/internal/testports"
)

func media(t description.MediaType, back bool) *description.Media {
	return &description.Media{Type: t, IsBackChannel: back}
}

func TestSelectKeepsVideoAndAudioOnlyWhenAllowed(t *testing.T) {
	video := media(description.MediaTypeVideo, false)
	audio := media(description.MediaTypeAudio, false)
	meta := media(description.MediaTypeApplication, false)
	talk := media(description.MediaTypeAudio, true) // back channel: the camera's speaker
	all := []*description.Media{video, audio, meta, talk}

	if got := Select(all, false); len(got) != 1 || got[0] != video {
		t.Fatalf("audio off: got %v, want video only", got)
	}
	got := Select(all, true)
	if len(got) != 2 || got[0] != video || got[1] != audio {
		t.Fatalf("audio on: got %v, want video and audio", got)
	}
	if got := Select([]*description.Media{audio}, false); len(got) != 0 {
		t.Fatalf("audio-only stream with audio off: got %v, want nothing", got)
	}
}

func TestBackoffDoublesUpToTheCapAndResets(t *testing.T) {
	b := backoff{min: time.Second, max: 5 * time.Second}
	want := []time.Duration{1, 2, 4, 5, 5}
	for i, w := range want {
		if got := b.next(); got != w*time.Second {
			t.Fatalf("step %d: %v, want %v", i, got, w*time.Second)
		}
	}
	b.reset()
	if got := b.next(); got != time.Second {
		t.Fatalf("after reset: %v, want 1s", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}
	c.applyDefaults()
	if c.MinBackoff != time.Second || c.MaxBackoff != 30*time.Second || c.StableAfter == 0 || c.Logger == nil {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestRunRejectsBadURLsWithoutSpinning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	Run(ctx, Config{Source: "::not a url", Target: "rtsp://127.0.0.1:1/x", MinBackoff: 100 * time.Millisecond})
	if time.Since(start) < 250*time.Millisecond {
		t.Fatal("Run returned before its context ended")
	}
}

// End to end in process: a fake camera serves video and audio; the target
// stands in for the engine. Only allowed media are set up at the camera and
// announced to the target, packets flow, and the pull comes back after the
// camera goes away.
func TestRunPublishesOnlyAllowedMediaAndReconnects(t *testing.T) {
	for _, audio := range []bool{false, true} {
		t.Run(fmt.Sprintf("audio=%v", audio), func(t *testing.T) {
			ports := testports.Get(t, 2)
			cam := startCamera(t, ports[0])
			target := startTarget(t, ports[1])

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, Config{
					Source:     fmt.Sprintf("rtsp://admin:pw@127.0.0.1:%d/cam", ports[0]),
					Target:     fmt.Sprintf("rtsp://hive:secret@127.0.0.1:%d/front/main", ports[1]),
					Audio:      audio,
					MinBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
				})
			}()

			wantMedia := 1
			if audio {
				wantMedia = 2
			}
			waitFor(t, 5*time.Second, func() bool { return target.packets(description.MediaTypeVideo) > 10 }, "video packets at the target")
			if got := cam.setups(); got != wantMedia {
				t.Fatalf("camera saw %d SETUPs, want %d", got, wantMedia)
			}
			if got := target.announced(); len(got) != wantMedia {
				t.Fatalf("target announced %v, want %d media", got, wantMedia)
			}
			if audio {
				waitFor(t, 5*time.Second, func() bool { return target.packets(description.MediaTypeAudio) > 10 }, "audio packets at the target")
			} else if n := target.packets(description.MediaTypeAudio); n != 0 {
				t.Fatalf("%d audio packets reached the target with audio off", n)
			}
			if path := target.path(); path != "/front/main" {
				t.Fatalf("published to %q", path)
			}

			// The camera disappears and comes back on the same address.
			cam.stop()
			before := target.announces()
			startCamera(t, ports[0])
			waitFor(t, 5*time.Second, func() bool { return target.announces() > before }, "re-publish after the camera came back")

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not stop after cancel")
			}
		})
	}
}

// fakeCamera serves one stream with an H.264 and a G.711 media.
type fakeCamera struct {
	srv    *gortsplib.Server
	stream *gortsplib.ServerStream
	quit   chan struct{}
	wg     sync.WaitGroup

	mu     sync.Mutex
	nSetup int
}

func startCamera(t *testing.T, port int) *fakeCamera {
	t.Helper()
	c := &fakeCamera{quit: make(chan struct{})}
	c.srv = &gortsplib.Server{Handler: c, RTSPAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	if err := c.srv.Start(); err != nil {
		t.Fatal(err)
	}
	desc := &description.Session{Medias: []*description.Media{
		{Type: description.MediaTypeVideo, Formats: []format.Format{&format.H264{
			PayloadTyp: 96, PacketizationMode: 1,
			SPS: []byte{0x67, 0x42, 0xc0, 0x1f, 0xd9, 0x00, 0x78, 0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20},
			PPS: []byte{0x68, 0xcb, 0x83, 0xcb, 0x20},
		}}},
		{Type: description.MediaTypeAudio, Formats: []format.Format{&format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1}}},
	}}
	stream := &gortsplib.ServerStream{Server: c.srv, Desc: desc}
	if err := stream.Initialize(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.stream = stream
	c.mu.Unlock()
	c.wg.Add(1)
	go c.write(desc)
	t.Cleanup(c.stop)
	return c
}

func (c *fakeCamera) write(desc *description.Session) {
	defer c.wg.Done()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var seq uint16
	for {
		select {
		case <-c.quit:
			return
		case <-tick.C:
		}
		seq++
		ts := uint32(seq) * 900
		_ = c.stream.WritePacketRTP(desc.Medias[0], &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: true},
			Payload: []byte{0x65, 0x88, 0x84, 0x00},
		})
		_ = c.stream.WritePacketRTP(desc.Medias[1], &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 0, SequenceNumber: seq, Timestamp: uint32(seq) * 80},
			Payload: make([]byte, 80),
		})
	}
}

func (c *fakeCamera) stop() {
	select {
	case <-c.quit:
		return
	default:
	}
	close(c.quit)
	c.wg.Wait()
	c.stream.Close()
	c.srv.Close()
}

func (c *fakeCamera) setups() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nSetup
}

func (c *fakeCamera) OnDescribe(*gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &base.Response{StatusCode: base.StatusOK}, c.stream, nil
}

func (c *fakeCamera) OnSetup(*gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nSetup++
	return &base.Response{StatusCode: base.StatusOK}, c.stream, nil
}

func (c *fakeCamera) OnPlay(*gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// fakeTarget accepts publishers like the engine does and counts packets.
type fakeTarget struct {
	mu        sync.Mutex
	medias    []description.MediaType
	byType    map[description.MediaType]int
	nAnnounce int
	lastPath  string
}

func startTarget(t *testing.T, port int) *fakeTarget {
	t.Helper()
	tg := &fakeTarget{byType: map[description.MediaType]int{}}
	srv := &gortsplib.Server{Handler: tg, RTSPAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return tg
}

func (tg *fakeTarget) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.nAnnounce++
	tg.lastPath = ctx.Path
	tg.medias = tg.medias[:0]
	for _, m := range ctx.Description.Medias {
		tg.medias = append(tg.medias, m.Type)
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (tg *fakeTarget) OnSetup(*gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil, nil
}

func (tg *fakeTarget) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	ctx.Session.OnPacketRTPAny(func(m *description.Media, _ format.Format, _ *rtp.Packet) {
		tg.mu.Lock()
		tg.byType[m.Type]++
		tg.mu.Unlock()
	})
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (tg *fakeTarget) packets(mt description.MediaType) int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.byType[mt]
}

func (tg *fakeTarget) announced() []description.MediaType {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]description.MediaType(nil), tg.medias...)
}

func (tg *fakeTarget) announces() int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.nAnnounce
}

func (tg *fakeTarget) path() string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.lastPath
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
