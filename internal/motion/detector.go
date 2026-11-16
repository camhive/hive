package motion

import (
	"slices"
	"time"

	"github.com/camhive/hive/internal/video"
)

// window is the detector's verdict on one closed window. Motion is set once
// Confirm active windows followed each other; Start is then the start of the
// first of them.
type window struct {
	Start, End time.Time
	Motion     bool
}

// detector reads P-frame sizes: a still scene compresses to small P-frames,
// movement makes them several times larger. The baseline is the 25th
// percentile of recent window averages, so it follows noise and light.
type detector struct {
	cfg Config

	start  time.Time // of the open window; zero before the first frame
	sum, n int       // P-frame bytes and count in the open window

	history     []float64 // window averages, oldest first
	streak      int       // consecutive active windows
	streakStart time.Time
}

// add consumes one frame and returns a verdict when it closes a window.
func (d *detector) add(f video.Frame) (window, bool) {
	if d.start.IsZero() {
		d.start = f.At
	}
	var w window
	closed := f.At.Sub(d.start) >= d.cfg.Window
	if closed {
		w = d.close(f.At)
	}
	if !f.Key {
		d.sum += f.Size
		d.n++
	}
	return w, closed
}

func (d *detector) close(now time.Time) window {
	w := window{Start: d.start, End: now}
	d.start = now
	if d.n == 0 { // only a keyframe: nothing to compare
		return w
	}
	avg := float64(d.sum) / float64(d.n)
	d.sum, d.n = 0, 0
	active := d.active(avg)
	d.remember(avg)
	if !active {
		d.streak = 0
		return w
	}
	if d.streak == 0 {
		d.streakStart = w.Start
	}
	d.streak++
	w.Start, w.Motion = d.streakStart, d.streak >= d.cfg.Confirm
	return w
}

func (d *detector) active(avg float64) bool {
	if len(d.history) < d.cfg.Warmup {
		return false
	}
	base := lowerQuartile(d.history)
	return avg >= base*d.cfg.Ratio && avg-base >= d.cfg.MinDelta
}

func (d *detector) remember(avg float64) {
	if len(d.history) == d.cfg.History {
		d.history = d.history[1:]
	}
	d.history = append(d.history, avg)
}

// lowerQuartile returns the 25th percentile of xs (not modified).
func lowerQuartile(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/4]
}
