package pull

import "testing"

// BenchmarkFrameReader measures what the frames motion source adds to the
// pull per second of sub stream (ADR-0020): reassembling and sizing 15
// access units.
func BenchmarkFrameReader(b *testing.B) {
	second := FrameSecond()
	b.ReportAllocs()
	for b.Loop() {
		second()
	}
}
