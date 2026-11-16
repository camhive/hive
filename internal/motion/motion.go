// Package motion turns signals into motion Events (ADR-0020). Each Camera has
// one Motion source: the camera's own detector (ONVIF events) when it offers
// one, otherwise the sizes of the compressed frames of one Stream. A Tracker
// per Camera debounces either into episodes; the Hub runs the Trackers and
// records the episodes as Events.
package motion

import "time"

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
	Window   time.Duration // frames are averaged per window
	History  int           // windows the baseline is taken from
	Warmup   int           // windows needed before anything is decided
	Ratio    float64       // a window is active at this multiple of the baseline …
	MinDelta float64       // … and at least this many bytes above it
	Confirm  int           // consecutive active windows that raise motion
	Hold     time.Duration // motion pausing for less than this continues the episode
	// MaxEpisode ends an episode that lasts this long; a camera stuck on
	// "motion" needs a fresh report to start the next.
	MaxEpisode time.Duration
}

// Defaults are the values ADR-0020 settles on.
func Defaults() Config {
	return Config{Window: time.Second, History: 120, Warmup: 10, Ratio: 2, MinDelta: 200, Confirm: 2, Hold: 10 * time.Second, MaxEpisode: 30 * time.Minute}
}

// Change is the beginning or the end of an episode.
type Change struct {
	Begin  bool
	At     time.Time
	Source Source // on a beginning
}
