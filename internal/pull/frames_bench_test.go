package pull

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

// gop is one second of a 640×360, 15 fps sub stream as a camera sends it:
// a 20 kB keyframe in 15 FU-A packets and 14 P-frames of 400 B.
func gop() []*rtp.Packet {
	var pkts []*rtp.Packet
	seq := uint16(0)
	add := func(ts uint32, marker bool, payload []byte) {
		seq++
		pkts = append(pkts, &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: marker}, Payload: payload})
	}
	const frag = 1400
	key := make([]byte, 20_000)
	for off := 0; off < len(key); off += frag {
		start, end := off == 0, off+frag >= len(key)
		hdr := byte(0x05)
		if start {
			hdr |= 0x80
		}
		if end {
			hdr |= 0x40
		}
		add(0, end, append([]byte{0x7c, hdr}, key[off:min(off+frag, len(key))]...))
	}
	for i := 1; i < 15; i++ {
		add(uint32(i*6000), true, append([]byte{0x41}, make([]byte, 399)...))
	}
	return pkts
}

// BenchmarkFrameReader measures what the frames motion source adds to the
// pull per second of sub stream (ADR-0020): reassembling and sizing 15
// access units.
func BenchmarkFrameReader(b *testing.B) {
	pkts := gop()
	r := newFrameReader(&format.H264{PayloadTyp: 96, PacketizationMode: 1}, func(time.Time, int, bool) {})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range pkts {
			r.packet(p)
		}
	}
}
