// Package update decides and carries out Hive updates (ADR-0018 refining
// ADR-0005): which signed bundle a Hive should run, when, and how it gets
// there with a rollback when the new version is not healthy.
package update

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Version is a release version: MAJOR.MINOR.PATCH with an optional
// pre-release; build metadata (dev builds) is not a release.
type Version struct {
	core [3]int
	pre  []string
}

// ParseVersion reads "1.2.3", "v1.2.3" or "1.2.3-rc.1".
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimPrefix(s, "v")
	core, pre, _ := strings.Cut(raw, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || strings.Contains(raw, "+") {
		return Version{}, fmt.Errorf("update: %q is not a release version", s)
	}
	var v Version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("update: %q is not a release version", s)
		}
		v.core[i] = n
	}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

// String prints the version without a leading v.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.core[0], v.core[1], v.core[2])
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

// Compare returns -1, 0 or 1; a pre-release sorts before its release.
func (v Version) Compare(o Version) int {
	for i := range v.core {
		if c := cmpInt(v.core[i], o.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		if c := cmpIdent(v.pre[i], o.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.pre), len(o.pre))
}

func cmpIdent(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return cmpInt(na, nb)
	case ea == nil: // numeric identifiers sort first
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// InRollout reports whether a Hive takes part in a rollout of percent: its
// id's hash falls in the first percent of the range, so a growing rollout
// keeps every Hive it already had.
func InRollout(hiveID string, percent int) bool {
	sum := sha256.Sum256([]byte(hiveID))
	return int(binary.BigEndian.Uint64(sum[:8])%100) < percent
}

// Window is the daily time span in the Hive's local time when updates may
// install; it may cross midnight.
type Window struct{ from, to int } // minutes of the day, from inclusive, to exclusive

// DefaultWindow is every night from 02:00 to 05:00.
const DefaultWindow = "02:00-05:00"

// ParseWindow reads "HH:MM-HH:MM"; empty means DefaultWindow.
func ParseWindow(s string) (Window, error) {
	if s == "" {
		s = DefaultWindow
	}
	a, b, ok := strings.Cut(s, "-")
	from, err1 := minuteOfDay(a)
	to, err2 := minuteOfDay(b)
	if !ok || err1 != nil || err2 != nil || from == to {
		return Window{}, fmt.Errorf("update: window %q must look like 02:00-05:00", s)
	}
	return Window{from: from, to: to}, nil
}

func minuteOfDay(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil || len(strings.TrimSpace(s)) != 5 {
		return 0, errors.New("bad time")
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether t (in its own location) falls in the window.
func (w Window) Contains(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if w.from < w.to {
		return m >= w.from && m < w.to
	}
	return m >= w.from || m < w.to
}

// Mode is how the owner wants updates.
type Mode string

// Modes; empty means Automatic.
const (
	Automatic Mode = "automatic"
	Manual    Mode = "manual"
)

// Target is a signed bundle the Hive could run.
type Target struct {
	Version    Version
	Security   bool    // installs outside the window
	Rollout    int     // percent of Hives that take it
	MinUpdater Version // the updater this bundle needs
}

// Situation is what the Hive knows when deciding.
type Situation struct {
	Hive    string
	Running Version
	Updater Version
	Failed  []Version // versions that rolled back here; never tried again
	Window  Window
	Mode    Mode
	Asked   bool // the owner asked to update now (manual mode)
	Now     time.Time
}

// Verdict is what to do with a target.
type Verdict int

// Verdicts.
const (
	UpToDate      Verdict = iota // nothing newer for this Hive
	Install                      // install now
	Wait                         // install in the next window
	Available                    // manual mode: tell the owner
	UpdaterTooOld                // the updater must update itself first
)

func (v Verdict) String() string {
	return [...]string{"up-to-date", "install", "wait", "available", "updater-too-old"}[v]
}

// Decide is the update policy (ADR-0018 §6, §7).
func Decide(s Situation, t Target) Verdict {
	failed := slices.ContainsFunc(s.Failed, func(f Version) bool { return f.Compare(t.Version) == 0 })
	switch {
	case t.Version.Compare(s.Running) <= 0 || failed || !InRollout(s.Hive, t.Rollout):
		return UpToDate
	case t.MinUpdater.Compare(s.Updater) > 0:
		return UpdaterTooOld
	case s.Mode == Manual && !s.Asked:
		return Available
	case s.Mode == Manual || t.Security || s.Window.Contains(s.Now):
		return Install
	}
	return Wait
}
