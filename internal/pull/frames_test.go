package pull

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/camhive/hive/internal/testports"
)

type frame struct {
	size int
	key  bool
}

type frames struct {
	mu  sync.Mutex
	got []frame
}

func (f *frames) add(_ time.Time, size int, key bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, frame{size, key})
}

func (f *frames) all() []frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]frame(nil), f.got...)
}

func pkt(seq uint16, ts uint32, marker bool, payload ...byte) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: marker}, Payload: payload}
}

func TestFrameReaderReportsAccessUnitSizesAndKeyframes(t *testing.T) {
	var got frames
	r := newFrameReader(&format.H264{PayloadTyp: 96, PacketizationMode: 1}, got.add)
	if r == nil {
		t.Fatal("H.264 must be readable")
	}
	r.packet(pkt(1, 0, true, 0x65, 1, 2, 3, 4, 5)) // IDR slice, 6 bytes
	// A P-slice split into two FU-A fragments: header 0x41 rebuilt, 4 + 3 payload bytes.
	r.packet(pkt(2, 3000, false, 0x5c, 0x81, 1, 2, 3, 4))
	r.packet(pkt(3, 3000, true, 0x5c, 0x41, 5, 6, 7))
	want := []frame{{6, true}, {8, false}}
	if g := got.all(); fmt.Sprint(g) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

func TestFrameReaderSkipsCodecsItCannotRead(t *testing.T) {
	if r := newFrameReader(&format.MJPEG{}, func(time.Time, int, bool) {}); r != nil {
		t.Fatal("MJPEG has no P-frames; it must not be read")
	}
}

func TestRunReportsFramesOfTheVideoMedia(t *testing.T) {
	ports := testports.Get(t, 2)
	startCamera(t, ports[0])
	startTarget(t, ports[1])
	var got frames
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, Config{
			Source:  fmt.Sprintf("rtsp://127.0.0.1:%d/cam", ports[0]),
			Target:  fmt.Sprintf("rtsp://hive:secret@127.0.0.1:%d/front/sub", ports[1]),
			Audio:   true,
			OnFrame: got.add,
		})
	}()
	waitFor(t, 3*time.Second, func() bool { return len(got.all()) > 10 }, "frames reported")
	cancel()
	<-done
	for _, f := range got.all() {
		if f.size != 4 || !f.key { // the fake camera sends 4-byte IDR slices; audio is not a frame
			t.Fatalf("frame %+v", f)
		}
	}
}
