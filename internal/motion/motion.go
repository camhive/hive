// Package motion turns signals into motion Events (ADR-0020). Each Camera has
// one Motion source: the camera's own detector (ONVIF events) when it offers
// one, otherwise the sizes of the compressed frames of one Stream. A Tracker
// per Camera debounces either into episodes; the Hub runs the Trackers and
// records the episodes as Events.
package motion

import (
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// Kind is the Event kind of motion.
const Kind = "motion"

// Source names where a Camera's motion comes from.
type Source string

// Motion sources.
const (
	SourceCamera Source = "camera" // the camera's ONVIF event service
	SourceFrames Source = "frames" // compressed frame sizes of one Stream
)

// Config tunes detection and debouncing.
type Config struct {
	Window  time.Duration // frames are averaged per window
	History int           // windows the baseline is taken from
	Warmup  int           // windows needed before anything is decided
	Level                 // the threshold and minimum episode length; a Tracker's SetLevel changes it
	// MinDelta: an active window is also at least this many bytes above the baseline.
	MinDelta float64
	Hold     time.Duration // motion pausing for less than this continues the episode
	// MaxEpisode ends an episode that lasts this long; a camera stuck on
	// "motion" needs a fresh report to start the next.
	MaxEpisode time.Duration
}

// Defaults are the values ADR-0020 settles on.
func Defaults() Config {
	return Config{Window: time.Second, History: 120, Warmup: 10, Level: levels[stepNormal], MinDelta: 200, Hold: 10 * time.Second, MaxEpisode: 30 * time.Minute}
}

// Level is how readily a Tracker raises motion (#157): the threshold of
// the frames source and the minimum episode length of either source.
type Level struct {
	Ratio   float64 // a window is active at this multiple of the baseline
	Confirm int     // consecutive active windows that raise motion (seconds at the default window)
	// CameraMin is how long the camera's own detector must report motion
	// before an episode begins; zero takes its report at once.
	CameraMin time.Duration
}

// levels are the steps from the most to the least sensitive: a camera's
// Sensitivity picks one, auto-calm takes the next. The last is reached
// only by calming a camera set to low. The one table of the mapping;
// docs/dev/hive-internals.md shows it.
var levels = []Level{
	{Ratio: 1.5, Confirm: 1},                           // high
	{Ratio: 2, Confirm: 2},                             // normal: ADR-0020
	{Ratio: 3, Confirm: 4, CameraMin: 3 * time.Second}, // low
	{Ratio: 4, Confirm: 6, CameraMin: 6 * time.Second}, // low, calmed
}

const stepNormal = 1

// LevelFor is the Level of a Sensitivity, one step less sensitive while
// the camera is calmed.
func LevelFor(s camera.Sensitivity, calmed bool) Level {
	var step int
	switch s {
	case camera.SensitivityHigh:
		step = 0
	case camera.SensitivityNormal:
		step = stepNormal
	case camera.SensitivityLow:
		step = 2
	}
	if !calmed {
		return levels[step]
	}
	l := levels[step+1]
	l.CameraMin = max(l.CameraMin, calmedCameraMin) // high calmed is normal, whose camera source takes reports at once
	return l
}

// calmedCameraMin is the shortest report a calmed Camera's own detector
// must send, so calming changes something for the camera source too.
const calmedCameraMin = time.Second

// pulseGap: camera reports of motion less than this apart are one report.
const pulseGap = time.Second

// Change is the beginning or the end of an episode.
type Change struct {
	Begin  bool
	At     time.Time
	Source Source // on a beginning
}
