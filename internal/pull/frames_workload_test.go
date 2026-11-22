package pull

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

// The benchmark workload is what its comment says: one second of a 15 fps
// sub stream, a 20 kB keyframe and 14 P-frames of 400 B.
func TestFrameSecondIsOneSecondOfSubStream(t *testing.T) {
	var sizes []int
	var keys int
	r := newFrameReader(&format.H264{PayloadTyp: 96, PacketizationMode: 1}, func(_ time.Time, size int, key bool) {
		sizes = append(sizes, size)
		if key {
			keys++
		}
	})
	for _, p := range subStreamSecond() {
		r.packet(p)
	}
	if len(sizes) != 15 || keys != 1 || sizes[0] != 20_001 || sizes[1] != 400 {
		t.Fatalf("%d frames, %d keyframes, sizes %v", len(sizes), keys, sizes)
	}
	FrameSecond()() // runs without a reader to report to
}
