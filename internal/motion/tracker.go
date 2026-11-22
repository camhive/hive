package motion

import (
	"time"

	"github.com/camhive/hive/internal/video"
)

// Tracker is one Camera's motion state: it reads frames and the camera's
// own motion reports, prefers the camera's while that source works, and
// debounces into episodes. Not safe for concurrent use; the Hub gives each
// Tracker one goroutine.
type Tracker struct {
	cfg Config
	det detector

	cameraLive bool      // the camera's event subscription works
	cameraOn   bool      // and the camera reports motion
	cameraFrom time.Time // since when, while no episode is open
	cameraOff  time.Time // when it last reported no motion

	on    bool      // an episode is open
	start time.Time // of the open episode
	last  time.Time // latest activity of the open episode
}

// NewTracker creates a Tracker with no episode open.
func NewTracker(cfg Config) *Tracker {
	return &Tracker{cfg: cfg, det: detector{cfg: cfg}}
}

// SetLevel changes the threshold and the minimum episode length; the
// baseline and an open episode stay.
func (t *Tracker) SetLevel(l Level) {
	t.cfg.Level = l
	t.det.cfg.Level = l
}

// Frame reads one frame of the analysed Stream. Frames keep the baseline
// current even while the camera's own events are used.
func (t *Tracker) Frame(f video.Frame) []Change {
	w, closed := t.det.add(f)
	if !closed || !w.Motion || t.cameraLive {
		return nil
	}
	return t.active(w.Start, w.End, SourceFrames)
}

// CameraSource says whether the camera's own motion events are available.
func (t *Tracker) CameraSource(live bool) {
	t.cameraLive = live
	if !live {
		t.cameraOn = false
	}
}

// CameraMotion is the camera reporting motion on or off at `at`.
// Pulses less than pulseGap apart count as one lasting report.
func (t *Tracker) CameraMotion(on bool, at time.Time) []Change {
	if on && !t.cameraOn && (t.cameraOff.IsZero() || at.Sub(t.cameraOff) >= pulseGap) {
		t.cameraFrom = at
	}
	if !on {
		t.cameraOff = at
	}
	t.cameraOn = on
	switch {
	case t.on:
		return t.active(at, at, SourceCamera)
	case !on, at.Sub(t.cameraFrom) < t.cfg.CameraMin: // a report shorter than CameraMin is no episode
		return nil
	case t.cfg.CameraMin == 0:
		return t.active(at, at, SourceCamera)
	}
	return t.active(t.cameraFrom, at, SourceCamera)
}

// Tick runs once a second: it ends an episode that reached MaxEpisode,
// extends motion the camera still reports and ends an episode that has been
// quiet for Hold, at its last activity.
func (t *Tracker) Tick(now time.Time) []Change {
	if t.on && now.Sub(t.start) >= t.cfg.MaxEpisode {
		t.on, t.cameraOn, t.cameraOff = false, false, time.Time{}
		return []Change{{At: t.start.Add(t.cfg.MaxEpisode)}}
	}
	if t.cameraLive && t.cameraOn {
		if !t.on && now.Sub(t.cameraFrom) < t.cfg.CameraMin {
			return nil
		}
		return t.active(t.cameraFrom, now, SourceCamera)
	}
	if t.on && now.Sub(t.last) >= t.cfg.Hold {
		t.on, t.cameraOff = false, time.Time{} // pulses before the end do not carry into the next episode
		return []Change{{At: t.last}}
	}
	return nil
}

// Open reports whether an episode is open.
func (t *Tracker) Open() bool { return t.on }

// Stop ends an open episode at its last activity: the Hive is shutting
// down and will measure afresh when it starts.
func (t *Tracker) Stop() []Change {
	if !t.on {
		return nil
	}
	t.on = false
	return []Change{{At: t.last}}
}

// active records activity from `from` to `to`, beginning an episode at
// `from` when none is open.
func (t *Tracker) active(from, to time.Time, src Source) []Change {
	if to.After(t.last) {
		t.last = to
	}
	if t.on {
		return nil
	}
	t.on, t.start = true, from
	return []Change{{Begin: true, At: from, Source: src}}
}
