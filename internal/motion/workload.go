package motion

import (
	"time"

	"github.com/camhive/hive/internal/video"
)

// TrackerSecond returns one second of the detector's work on a 15 fps
// stream (ADR-0020): 15 frames and the window close with its percentile.
// Each call does one second's work. BenchmarkTrackerOneSecond and
// tools/sizing (#142) measure it.
func TrackerSecond() func() {
	const fps = 15
	tr := NewTracker(Defaults())
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	return func() {
		for i := range fps {
			at = at.Add(time.Second / fps)
			tr.Frame(video.Frame{At: at, Size: 400 + i, Key: i == 0})
		}
		tr.Tick(at)
	}
}
