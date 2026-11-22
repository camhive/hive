package motion

import (
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// Auto-calm (#157): a camera that raises more than Limit episodes within
// Window is calmed, one Level less sensitive than its Sensitivity, until
// Quiet passes without an episode. Its episodes keep becoming Events; only
// fewer of them begin.

// CalmRule tunes auto-calm; zero fields take the defaults.
type CalmRule struct {
	Limit  int           // episodes within Window that are still fine; default 20
	Window time.Duration // default 1h
	Quiet  time.Duration // without an episode for this long the camera steps back; default 1h
}

func (r CalmRule) withDefaults() CalmRule {
	if r.Limit == 0 {
		r.Limit = 20
	}
	if r.Window == 0 {
		r.Window = time.Hour
	}
	if r.Quiet == 0 {
		r.Quiet = time.Hour
	}
	return r
}

// Calming is a camera calmed or stepping back, for the action log.
type Calming struct {
	Camera      string
	Calmed      bool      // false: stepped back to its Sensitivity
	At          time.Time // the episode that calmed it, or the end of the quiet spell
	Episodes    int       // episodes within the rule's window when calmed
	Sensitivity camera.Sensitivity
}

// calmState is one camera's recent episode starts and since when it is calmed.
type calmState struct {
	begins []time.Time // within the window, oldest first
	since  time.Time   // zero: not calmed
}

// begin records an episode start and reports whether it calms the camera
// and how many episodes the window holds.
func (s *calmState) begin(at time.Time, r CalmRule) (bool, int) {
	keep := 0
	for keep < len(s.begins) && at.Sub(s.begins[keep]) >= r.Window {
		keep++
	}
	s.begins = append(s.begins[keep:], at)
	if !s.since.IsZero() || len(s.begins) <= r.Limit {
		return false, len(s.begins)
	}
	s.since = at
	return true, len(s.begins)
}

// quiet reports whether a calmed camera has had no episode for the rule's
// Quiet and steps it back.
func (s *calmState) quiet(now time.Time, r CalmRule) bool {
	if s.since.IsZero() || now.Sub(s.begins[len(s.begins)-1]) < r.Quiet {
		return false
	}
	s.since = time.Time{}
	return true
}
