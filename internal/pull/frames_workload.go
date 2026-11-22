package pull

import (
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

// FrameSecond returns one second of the frames motion source's work
// (ADR-0020): a 640×360, 15 fps H.264 sub stream as a camera sends it, a
// 20 kB keyframe in 15 FU-A packets and 14 P-frames of 400 B (about
// 205 kbit/s), reassembled and sized by the pull's frame reader. Each call
// does one second's work. BenchmarkFrameReader measures it.
func FrameSecond() func() { return FramesOf(subStreamSecond()) }

// FramesOf returns the frames motion source's work on the given H.264 RTP
// packets: each call reassembles and sizes all of them, as the pull does
// for the stream it analyses. tools/sizing (#142) measures it on a real
// sub stream clip.
func FramesOf(pkts []*rtp.Packet) func() {
	r := newFrameReader(&format.H264{PayloadTyp: 96, PacketizationMode: 1}, func(time.Time, int, bool) {})
	return func() {
		for _, p := range pkts {
			r.packet(p)
		}
	}
}

// subStreamSecond is one second of the sub stream as RTP packets.
func subStreamSecond() []*rtp.Packet {
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
