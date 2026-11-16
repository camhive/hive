package motion_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/video"
)

// BenchmarkTrackerOneSecond measures the detector per second of a 15 fps
// stream (ADR-0020): 15 frames and the window close with its percentile.
func BenchmarkTrackerOneSecond(b *testing.B) {
	tr := motion.NewTracker(motion.Defaults())
	at := t0
	b.ReportAllocs()
	for b.Loop() {
		for i := range fps {
			at = at.Add(time.Second / fps)
			tr.Frame(video.Frame{At: at, Size: 400 + i, Key: i == 0})
		}
		tr.Tick(at)
	}
}
