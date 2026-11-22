package archivekey_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/archivekey"
)

// fakeKey is the archive key as the autolock sees it.
type fakeKey struct {
	state archivekey.State
	locks int
}

func (k *fakeKey) State() archivekey.State { return k.state }
func (k *fakeKey) Lock()                   { k.state = archivekey.Locked; k.locks++ }

// clock is a wall clock and a monotonic clock the test moves by hand,
// together (pass) or one alone (a suspend, a clock set by hand).
type clock struct {
	wall time.Time
	mono time.Duration
}

func (c *clock) now() time.Time           { return c.wall }
func (c *clock) since() time.Duration     { return c.mono }
func (c *clock) pass(d time.Duration)     { c.wall, c.mono = c.wall.Add(d), c.mono+d }
func (c *clock) wallOnly(d time.Duration) { c.wall = c.wall.Add(d) }
func (c *clock) monoOnly(d time.Duration) { c.mono += d }
func newClock() *clock                    { return &clock{wall: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)} }
func unlockedKey() *fakeKey               { return &fakeKey{state: archivekey.Unlocked} }
func autolock(k *fakeKey, c *clock, s archivekey.AutolockSettings) *archivekey.Autolock {
	a := archivekey.NewAutolock(k, archivekey.WithAutolockClock(c.now, c.since))
	a.Set(s)
	a.Unlocked()
	return a
}

func wantNoLock(t *testing.T, what string, a *archivekey.Autolock, k *fakeKey) {
	t.Helper()
	if r, ok := a.Check(); ok || k.locks != 0 {
		t.Fatalf("%s: locked (%s), want still unlocked", what, r)
	}
}

func wantLock(t *testing.T, what string, a *archivekey.Autolock, k *fakeKey, reason archivekey.Reason) {
	t.Helper()
	if r, ok := a.Check(); !ok || r != reason || k.locks != 1 || k.state != archivekey.Locked {
		t.Fatalf("%s: Check = %q %v, locks %d, want one lock for %s", what, r, ok, k.locks, reason)
	}
}

// The inactivity timer counts from the unlock and from the last playback,
// export or sound request: activity pushes the lock back.
func TestTheArchiveLocksOnlyAfterRealInactivity(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{After: time.Hour})
	if at, r := a.LocksAt(); !at.Equal(c.wall.Add(time.Hour)) || r != archivekey.ReasonInactivity {
		t.Fatalf("LocksAt = %v %s, want an hour after the unlock", at, r)
	}
	c.pass(50 * time.Minute)
	a.Touch() // a playback
	c.pass(50 * time.Minute)
	wantNoLock(t, "100 min after the unlock, 50 after a playback", a, k)
	c.pass(10 * time.Minute)
	wantLock(t, "an hour after the playback", a, k, archivekey.ReasonInactivity)
	if at, _ := a.LocksAt(); !at.IsZero() {
		t.Fatalf("a locked archive has no lock time: %v", at)
	}
	wantNoSecondLock(t, a)
}

func wantNoSecondLock(t *testing.T, a *archivekey.Autolock) {
	t.Helper()
	if _, ok := a.Check(); ok {
		t.Fatal("a locked archive locked again")
	}
}

// Leaving home starts the grace period; a return within it cancels the lock.
func TestThePresenceLockWaitsTheGracePeriodAndAReturnCancelsIt(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{Away: 10 * time.Minute})
	if at, _ := a.LocksAt(); !at.IsZero() {
		t.Fatalf("at home, no timer: %v", at)
	}
	a.Presence(false)
	c.pass(5 * time.Minute)
	a.Presence(false) // repeated reports do not restart the grace period
	if at, r := a.LocksAt(); !at.Equal(c.wall.Add(5*time.Minute)) || r != archivekey.ReasonAway {
		t.Fatalf("LocksAt = %v %s, want 10 min after leaving", at, r)
	}
	c.pass(4 * time.Minute)
	a.Presence(true) // back within the grace period
	c.pass(time.Hour)
	wantNoLock(t, "back home", a, k)

	a.Presence(false)
	c.pass(9 * time.Minute)
	wantNoLock(t, "within the grace period", a, k)
	c.pass(time.Minute)
	wantLock(t, "away past the grace period", a, k, archivekey.ReasonAway)
}

// An unlock from outside, or switching the setting on while away, gives the
// owner the grace period again instead of locking at once.
func TestAnUnlockWhileAwayStartsTheGracePeriodAgain(t *testing.T) {
	k, c := &fakeKey{state: archivekey.Locked}, newClock()
	a := archivekey.NewAutolock(k, archivekey.WithAutolockClock(c.now, c.since))
	a.Presence(false)
	c.pass(3 * time.Hour)
	a.Set(archivekey.AutolockSettings{Away: time.Hour})
	k.state = archivekey.Unlocked
	a.Unlocked()
	c.pass(59 * time.Minute)
	wantNoLock(t, "59 min after the remote unlock", a, k)

	a.Set(archivekey.AutolockSettings{Away: time.Hour, After: 24 * time.Hour}) // saved again, away unchanged
	c.pass(time.Minute)
	wantLock(t, "an hour after the remote unlock, though saved again", a, k, archivekey.ReasonAway)
}

// Saving the settings again does not push the away lock back: only
// switching it on while away starts the grace period.
func TestSettingTheSameAwayValueDoesNotRestartTheGracePeriod(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{Away: 10 * time.Minute})
	a.Presence(false)
	for range 3 {
		c.pass(3 * time.Minute)
		a.Set(archivekey.AutolockSettings{Away: 10 * time.Minute})
		a.Set(archivekey.AutolockSettings{Away: 10 * time.Minute, After: time.Hour})
	}
	c.pass(time.Minute)
	wantLock(t, "10 min after leaving, however often it was saved", a, k, archivekey.ReasonAway)
}

// Switching a timer off while it runs stops it.
func TestSwitchingOffStopsARunningTimer(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{After: time.Hour, Away: 10 * time.Minute})
	a.Presence(false)
	c.pass(9 * time.Minute)
	a.Set(archivekey.AutolockSettings{})
	if at, _ := a.LocksAt(); !at.IsZero() {
		t.Fatalf("both off: LocksAt %v", at)
	}
	c.pass(48 * time.Hour)
	wantNoLock(t, "switched off", a, k)
}

// The monotonic clock stops while the machine sleeps; the wall clock can be
// set back. The timer is due when either clock says so.
func TestATimerIsDueByEitherClock(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{After: time.Hour})
	c.pass(10 * time.Minute)
	c.wallOnly(3 * 24 * time.Hour) // slept three days
	if at, _ := a.LocksAt(); at.After(c.wall) {
		t.Fatalf("after the sleep LocksAt %v is later than now %v", at, c.wall)
	}
	wantLock(t, "after a sleep past the timer", a, k, archivekey.ReasonInactivity)

	k, c = unlockedKey(), newClock()
	a = autolock(k, c, archivekey.AutolockSettings{Away: 10 * time.Minute})
	a.Presence(false)
	c.wallOnly(-2 * time.Hour) // the clock was set back
	c.monoOnly(10 * time.Minute)
	wantLock(t, "10 real minutes away, the wall clock set back", a, k, archivekey.ReasonAway)
}

func TestOnlyTheChoicesAreValid(t *testing.T) {
	for _, s := range []archivekey.AutolockSettings{{}, {After: time.Hour, Away: 10 * time.Minute}, {After: 7 * 24 * time.Hour, Away: time.Hour}} {
		if !s.Valid() {
			t.Errorf("%+v invalid", s)
		}
	}
	for _, s := range []archivekey.AutolockSettings{{After: 2 * time.Hour}, {Away: time.Minute}, {After: -time.Hour}} {
		if s.Valid() {
			t.Errorf("%+v valid", s)
		}
	}
}

// Off counts as never: switching a timer off or lengthening it weakens.
func TestWhatWeakensTheLock(t *testing.T) {
	h, m10 := time.Hour, 10*time.Minute
	for _, c := range []struct {
		was, next archivekey.AutolockSettings
		weakens   bool
	}{
		{archivekey.AutolockSettings{}, archivekey.AutolockSettings{After: h, Away: m10}, false},
		{archivekey.AutolockSettings{After: 8 * h}, archivekey.AutolockSettings{After: h}, false},
		{archivekey.AutolockSettings{Away: h}, archivekey.AutolockSettings{Away: m10}, false},
		{archivekey.AutolockSettings{After: h}, archivekey.AutolockSettings{After: h}, false},
		{archivekey.AutolockSettings{After: h}, archivekey.AutolockSettings{After: 8 * h}, true},
		{archivekey.AutolockSettings{After: h}, archivekey.AutolockSettings{}, true},
		{archivekey.AutolockSettings{Away: m10}, archivekey.AutolockSettings{Away: h}, true},
		{archivekey.AutolockSettings{Away: m10, After: 8 * h}, archivekey.AutolockSettings{After: h}, true},
	} {
		if got := c.was.Weakens(c.next); got != c.weakens {
			t.Errorf("%+v -> %+v: weakens %v, want %v", c.was, c.next, got, c.weakens)
		}
	}
}

// Whichever timer is due first locks, with its own reason.
func TestTheEarlierTimerWins(t *testing.T) {
	k, c := unlockedKey(), newClock()
	a := autolock(k, c, archivekey.AutolockSettings{After: time.Hour, Away: 10 * time.Minute})
	c.pass(55 * time.Minute)
	a.Presence(false)
	if at, r := a.LocksAt(); !at.Equal(c.wall.Add(5*time.Minute)) || r != archivekey.ReasonInactivity {
		t.Fatalf("LocksAt = %v %s, want the inactivity lock first", at, r)
	}
	c.pass(5 * time.Minute)
	wantLock(t, "inactivity first", a, k, archivekey.ReasonInactivity)
}

// Nothing locks while both settings are off, or while the archive is not
// unlocked (off, or locked by hand already).
func TestNoAutomaticLockWhileOffOrLocked(t *testing.T) {
	c := newClock()
	k := unlockedKey()
	a := autolock(k, c, archivekey.AutolockSettings{})
	a.Presence(false)
	c.pass(30 * 24 * time.Hour)
	wantNoLock(t, "settings off", a, k)

	for _, s := range []archivekey.State{archivekey.Off, archivekey.Locked} {
		k := &fakeKey{state: s}
		a := autolock(k, c, archivekey.AutolockSettings{After: time.Hour, Away: 10 * time.Minute})
		a.Presence(false)
		c.pass(2 * time.Hour)
		if at, _ := a.LocksAt(); !at.IsZero() {
			t.Fatalf("%s: LocksAt %v", s, at)
		}
		wantNoLock(t, string(s), a, k)
	}
}

func TestSettingsReadBack(t *testing.T) {
	a := archivekey.NewAutolock(unlockedKey())
	if s := a.Settings(); s != (archivekey.AutolockSettings{}) {
		t.Fatalf("default %v, want off", s)
	}
	want := archivekey.AutolockSettings{After: 8 * time.Hour, Away: time.Hour}
	a.Set(want)
	if s := a.Settings(); s != want {
		t.Fatalf("Settings = %v, want %v", s, want)
	}
}

// #157: the hive marks motion quiet while the owner is at home. At home is
// a fresh report only: the app reports every minute while it is open, so a
// closed app or a phone that left without saying so counts as away within
// HomeFresh. Unknown (nothing reported, after a restart) is not at home.
func TestAtHomeIsAFreshAtHomeReport(t *testing.T) {
	c := newClock()
	a := autolock(&fakeKey{}, c, archivekey.AutolockSettings{}) // the archive need not be encrypted
	if a.AtHome() {
		t.Fatal("at home before any report")
	}
	a.Presence(true)
	c.pass(archivekey.HomeFresh - time.Second)
	if !a.AtHome() {
		t.Fatal("not at home right after the report")
	}
	a.Presence(true) // the heartbeat
	c.pass(archivekey.HomeFresh - time.Second)
	if !a.AtHome() {
		t.Fatal("the heartbeat did not keep it")
	}
	c.pass(2 * time.Second)
	if a.AtHome() {
		t.Fatal("a stale report still counts")
	}
	a.Presence(true)
	a.Presence(false)
	if a.AtHome() {
		t.Fatal("away still at home")
	}
}
