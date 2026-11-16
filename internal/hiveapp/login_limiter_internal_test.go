package hiveapp

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiterLocksASourceAndLetsItGoLater(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	l := newLoginLimiter(func() time.Time { return now })
	for i := range LocalLoginLockout.MaxFailures {
		now = now.Add(time.Second)
		a, refused := l.reserve("192.168.1.20")
		if !refused.IsZero() {
			t.Fatalf("refused after %d failures", i)
		}
		if (i == LocalLoginLockout.MaxFailures-1) != !a.locks.IsZero() {
			t.Fatalf("attempt %d: locks = %v", i+1, a.locks)
		}
	}
	if _, refused := l.reserve("192.168.1.20"); !refused.Equal(now.Add(LocalLoginLockout.LockFor)) {
		t.Fatalf("refused until %v", refused)
	}
	if _, refused := l.reserve("192.168.1.21"); !refused.IsZero() {
		t.Fatal("another address must not be locked by one source")
	}
	now = now.Add(LocalLoginLockout.LockFor)
	if _, refused := l.reserve("192.168.1.20"); !refused.IsZero() {
		t.Fatal("the lock must end")
	}
}

func TestLoginLimiterForgetsASourcesFailuresAfterItsSuccess(t *testing.T) {
	now := time.Now()
	l := newLoginLimiter(func() time.Time { return now })
	for range LocalLoginLockout.MaxFailures - 1 {
		l.reserve("10.0.0.2")
	}
	a, _ := l.reserve("10.0.0.2")
	l.succeeded(a)
	if a, refused := l.reserve("10.0.0.2"); !refused.IsZero() || !a.locks.IsZero() {
		t.Fatal("a success must clear the source's earlier failures")
	}
}

// The owner signing in during a spread-out guess must not reset the count
// of everyone else's failures.
func TestLoginLimiterSuccessLeavesOtherSourcesFailuresCounted(t *testing.T) {
	now := time.Now()
	l := newLoginLimiter(func() time.Time { return now })
	for i := range localLoginLockoutAll.MaxFailures - 1 {
		l.reserve(fmt.Sprintf("10.0.1.%d", i))
	}
	owner, _ := l.reserve("10.0.2.1")
	l.succeeded(owner)
	l.reserve("10.0.3.1") // the fiftieth failure
	if _, refused := l.reserve("10.0.4.1"); refused.IsZero() {
		t.Fatal("the owner's success cleared the failures of other sources")
	}
}

func TestLoginLimiterStopsAGuessSpreadOverManyAddresses(t *testing.T) {
	now := time.Now()
	l := newLoginLimiter(func() time.Time { return now })
	for i := range localLoginLockoutAll.MaxFailures {
		l.reserve(fmt.Sprintf("10.0.1.%d", i))
	}
	if _, refused := l.reserve("10.0.9.9"); refused.IsZero() {
		t.Fatal("after many failures from everywhere, every source waits")
	}
	now = now.Add(localLoginLockoutAll.Window + localLoginLockoutAll.LockFor)
	a, _ := l.reserve("10.0.9.9")
	l.succeeded(a)
	if len(l.attempts) != 0 {
		t.Fatalf("old attempts must be forgotten: %v", l.attempts)
	}
}

func TestSourceKeyGroupsAnIPv6Slash64(t *testing.T) {
	cases := map[string]string{
		"192.168.1.20":             "192.168.1.20",
		"2001:db8:1:2:aaaa::1":     "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb:cc::99": "2001:db8:1:2::/64",
		"fe80::1":                  "fe80::/64",
		"not-an-ip":                "not-an-ip",
	}
	for in, want := range cases {
		if got := sourceKey(in); got != want {
			t.Errorf("sourceKey(%q) = %q, want %q", in, got, want)
		}
	}
}
