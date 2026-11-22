package motion_test

import (
	"testing"

	"github.com/camhive/hive/internal/motion"
)

// BenchmarkTrackerOneSecond measures the detector per second of a 15 fps
// stream (ADR-0020): 15 frames and the window close with its percentile.
func BenchmarkTrackerOneSecond(b *testing.B) {
	second := motion.TrackerSecond()
	b.ReportAllocs()
	for b.Loop() {
		second()
	}
}
