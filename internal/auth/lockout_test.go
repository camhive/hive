package auth_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
)

func TestLockoutPolicyHasItsOwnThresholds(t *testing.T) {
	p := auth.Lockout{MaxFailures: 2, Window: time.Minute, LockFor: 5 * time.Minute}
	now := time.Now()
	if !p.LockedUntil([]auth.Attempt{{At: now}}, now).IsZero() {
		t.Fatal("one failure below the limit locked")
	}
	two := []auth.Attempt{{At: now}, {At: now.Add(time.Second)}}
	if until := p.LockedUntil(two, now.Add(time.Second)); !until.Equal(now.Add(time.Second + 5*time.Minute)) {
		t.Fatalf("locked until %v", until)
	}
	cleared := []auth.Attempt{two[0], two[1], {At: now.Add(2 * time.Second), Success: true}}
	if !p.LockedUntil(cleared, now.Add(3*time.Second)).IsZero() {
		t.Fatal("a success must clear the failures")
	}
}
