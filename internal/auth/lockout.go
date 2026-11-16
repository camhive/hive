package auth

import "time"

// Lockout is a lockout policy: after MaxFailures failed logins within
// Window, refuse until LockFor has passed since the last failure. Cloud
// accounts (internal/domain/account) and the hive's local login (#57) each
// set their own thresholds.
type Lockout struct {
	MaxFailures int
	Window      time.Duration
	LockFor     time.Duration
}

// Attempt is one login attempt.
type Attempt struct {
	At      time.Time
	Success bool
}

// LockedUntil returns the zero time when the next attempt may go ahead, or
// the moment the lock ends. A success clears earlier failures.
func (l Lockout) LockedUntil(attempts []Attempt, now time.Time) time.Time {
	var failures int
	var last time.Time
	for _, a := range attempts {
		if a.Success {
			failures, last = 0, time.Time{}
			continue
		}
		if now.Sub(a.At) <= l.Window {
			failures++
			if a.At.After(last) {
				last = a.At
			}
		}
	}
	if failures >= l.MaxFailures {
		return last.Add(l.LockFor)
	}
	return time.Time{}
}
