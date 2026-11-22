package archivekey

import (
	"sync"
	"time"
)

// Automatic locking (ADR-0019, addendum #147): the unlocked archive locks
// itself after a time without footage being read, or a grace period after
// the owner's app reported that it left home. The Hive alone decides; the
// presence report is advisory and the inactivity timer the fallback.

// AutolockSettings are the owner's two settings; zero is off.
type AutolockSettings struct {
	After time.Duration // inactivity: no playback, sound or export for this long
	Away  time.Duration // grace period after the owner left home
}

// Choice is one value of a setting and its name in the API.
type Choice struct {
	Name string
	D    time.Duration // 0 is off
}

// The values the owner may choose; the one list of them.
var (
	AfterChoices = []Choice{{"off", 0}, {"1h", time.Hour}, {"8h", 8 * time.Hour}, {"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour}}
	AwayChoices  = []Choice{{"off", 0}, {"10m", 10 * time.Minute}, {"1h", time.Hour}}
)

func chosen(cs []Choice, d time.Duration) bool {
	for _, c := range cs {
		if c.D == d {
			return true
		}
	}
	return false
}

// Valid says whether both values are among the choices.
func (s AutolockSettings) Valid() bool {
	return chosen(AfterChoices, s.After) && chosen(AwayChoices, s.Away)
}

// Weakens says whether next locks later than s in some case: a timer
// switched off or made longer. Off counts as never.
func (s AutolockSettings) Weakens(next AutolockSettings) bool {
	return later(s.After, next.After) || later(s.Away, next.Away)
}

func later(was, now time.Duration) bool {
	switch {
	case was == now:
		return false
	case now == 0:
		return true
	case was == 0:
		return false
	default:
		return now > was
	}
}

// Reason says which timer locked the archive.
type Reason string

// Reasons, as audited (archive.encryption.lock).
const (
	ReasonInactivity Reason = "inactivity"
	ReasonAway       Reason = "away"
)

// Locker is the archive key as the autolock uses it (Vault).
type Locker interface {
	State() State
	Lock()
}

// instant is one reading of both clocks. The monotonic clock stops while
// the machine sleeps on some systems; the wall clock can be set back. A
// timer is due when either says so.
type instant struct {
	wall time.Time     // without a monotonic reading
	mono time.Duration // since the Autolock was made
}

// elapsed is the longer of the two clocks' readings since i.
func (i instant) elapsed(now instant) time.Duration {
	return max(now.mono-i.mono, now.wall.Sub(i.wall))
}

// Autolock keeps the timers of one archive key. Memory only: after a
// restart the archive is locked anyway.
type Autolock struct {
	key  Locker
	wall func() time.Time
	mono func() time.Duration

	mu     sync.Mutex
	s      AutolockSettings
	active instant  // the last read of footage, or the unlock
	away   *instant // since when the owner is away; nil while at home or unknown
	home   *instant // the latest at-home report; nil after an away report or none
}

// HomeFresh is how long an at-home report counts for AtHome (#157). The app
// repeats it every minute while it is open.
const HomeFresh = 5 * time.Minute

// AutolockOption customises an Autolock.
type AutolockOption func(*Autolock)

// WithAutolockClock sets the wall clock and the monotonic clock (time since
// the start) the timers count with.
func WithAutolockClock(wall func() time.Time, mono func() time.Duration) AutolockOption {
	return func(a *Autolock) { a.wall, a.mono = wall, mono }
}

// NewAutolock starts with both settings off.
func NewAutolock(key Locker, opts ...AutolockOption) *Autolock {
	start := time.Now()
	a := &Autolock{key: key, wall: time.Now, mono: func() time.Duration { return time.Since(start) }}
	for _, o := range opts {
		o(a)
	}
	a.active = a.now()
	return a
}

func (a *Autolock) now() instant { return instant{wall: a.wall().Round(0), mono: a.mono()} }

// Settings are the current settings.
func (a *Autolock) Settings() AutolockSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.s
}

// Set changes the settings. Switching the away lock on while the owner is
// away starts its grace period then, so it does not lock at once; any other
// change leaves the running timers as they are.
func (a *Autolock) Set(s AutolockSettings) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.s.Away == 0 && s.Away > 0 {
		a.restartAway()
	}
	a.s = s
}

// Touch records a read of footage: a playback, its sound or an export.
func (a *Autolock) Touch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active = a.now()
}

// Unlocked records the unlock: it counts as activity, and while the owner
// is away the grace period starts again.
func (a *Autolock) Unlocked() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active = a.now()
	a.restartAway()
}

// Presence records the owner's report: leaving starts the grace period
// (repeated reports keep its start), a return cancels it.
func (a *Autolock) Presence(atHome bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if atHome {
		now := a.now()
		a.home = &now
	} else {
		a.home = nil
	}
	switch {
	case atHome:
		a.away = nil
	case a.away == nil:
		now := a.now()
		a.away = &now
	}
}

// AtHome says whether the owner's app reported at home within HomeFresh:
// the Hive then marks motion it sends to the cloud quiet (#157, ADR-0021
// addendum). Where the owner is never leaves the house.
func (a *Autolock) AtHome() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.home != nil && a.home.elapsed(a.now()) < HomeFresh
}

// restartAway starts the grace period again while away; the caller holds mu.
func (a *Autolock) restartAway() {
	if a.away != nil {
		now := a.now()
		a.away = &now
	}
}

// LocksAt is when the archive will lock itself and why, on the wall clock;
// zero when no timer runs (both settings off, at home, or the archive not
// unlocked). It may be past: the lock is due.
func (a *Autolock) LocksAt() (time.Time, Reason) {
	left, why, ok := a.left()
	if !ok {
		return time.Time{}, ""
	}
	return a.wall().Round(0).Add(left), why
}

// left is the time until the earlier timer is due.
func (a *Autolock) left() (time.Duration, Reason, bool) {
	if a.key.State() != Unlocked {
		return 0, "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	var left time.Duration
	var why Reason
	if a.s.After > 0 {
		left, why = a.s.After-a.active.elapsed(now), ReasonInactivity
	}
	if a.s.Away > 0 && a.away != nil {
		if l := a.s.Away - a.away.elapsed(now); why == "" || l < left {
			left, why = l, ReasonAway
		}
	}
	return left, why, why != ""
}

// Check locks the archive when a timer is due and says why.
func (a *Autolock) Check() (Reason, bool) {
	left, why, ok := a.left()
	if !ok || left > 0 {
		return "", false
	}
	a.key.Lock()
	return why, true
}
