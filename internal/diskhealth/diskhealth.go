// Package diskhealth watches the health of the disk the archive is on
// (ADR-0024 §6, #155): SMART through smartctl, the kernel's I/O errors on
// that device, the hive's own write errors and the engine's segment write
// errors, and a small probe write. Linux first; elsewhere the state is
// unknown unless the hive's own writes fail. The verdict is a pure
// function of one check's readings (Evaluate); the Monitor runs a check at
// start and every 30 minutes, never more often, and turns verdicts into
// the disk.failing and disk.failed episodes.
package diskhealth

import (
	"encoding/json"
	"time"
)

// State is the verdict on the archive disk.
type State string

// States.
const (
	Unknown State = "unknown" // nothing could be read (no smartctl, no journal, not Linux)
	OK      State = "ok"
	Failing State = "failing" // replace the disk soon
	Failed  State = "failed"  // the archive cannot be written: recording has stopped
)

// Signal names where a piece of evidence comes from.
type Signal string

// Signals.
const (
	SignalSMART  Signal = "smart"  // smartctl -j
	SignalKernel Signal = "kernel" // I/O errors in the kernel log on the archive's device
	SignalWrites Signal = "writes" // the hive's and the engine's own write errors
	SignalProbe  Signal = "probe"  // a test write into the archive directory failed
	SignalDevice Signal = "device" // the archive directory or its device vanished or changed (#172)
)

// Evidence is one reason for the verdict, with the numbers behind it.
type Evidence struct {
	Signal Signal           `json:"signal"`
	Detail string           `json:"detail,omitempty"`
	Values map[string]int64 `json:"values,omitempty"`
}

// Report is the last check.
type Report struct {
	State        State      `json:"state"`
	CheckedAt    time.Time  `json:"checked_at"` // zero before the first check
	Device       string     `json:"device,omitempty"`
	TemperatureC *int       `json:"temperature_c,omitempty"`
	Evidence     []Evidence `json:"evidence"`
}

// Thresholds of SMART attributes that count as evidence. Reallocated
// sectors never go down, and a few are common on old healthy disks, so
// only a large number counts; any pending or uncorrectable sector does.
const (
	ReallocatedLimit = 50
	WornPercent      = 100 // NVMe percentage_used: the rated endurance is used up
)

// Sample is what one check read. A nil reading was not available.
type Sample struct {
	SMART       *SMART
	Kernel      *Kernel
	Writes      Writes
	Probe       Class    // the probe write's failure; None when it worked or told nothing
	ProbeFailed bool     // the probe write failed for any reason
	Lost        []string // the archive directory or its device vanished or changed since it was seen (#172)
}

// Evaluate is the verdict on one check. Failed when the archive's
// directory or device was lost or the probe could not write for a reason
// of the disk; failing on any evidence; ok only when SMART or the kernel
// log could be read and showed nothing; unknown otherwise.
func Evaluate(s Sample) (State, []Evidence) {
	var ev []Evidence
	for _, l := range s.Lost {
		ev = append(ev, Evidence{Signal: SignalDevice, Detail: l})
	}
	if s.SMART != nil {
		ev = append(ev, s.SMART.evidence()...)
	}
	if s.Kernel != nil && s.Kernel.Errors > 0 {
		ev = append(ev, s.Kernel.evidence())
	}
	if s.Writes.bad() {
		ev = append(ev, s.Writes.evidence())
	}
	if s.Probe.disk() {
		ev = append(ev, Evidence{Signal: SignalProbe, Detail: "a test write into the archive failed: " + string(s.Probe)})
	}
	if s.Probe.disk() || len(s.Lost) > 0 {
		return Failed, ev
	}
	switch {
	case len(ev) > 0:
		return Failing, ev
	case s.SMART != nil || s.Kernel != nil:
		return OK, nil
	}
	return Unknown, nil
}

// Healthy is how long a disk must look healthy before disk.failing ends.
const Healthy = 24 * time.Hour

// clean says a check read everything a kernel-only episode needs to end
// (#172): SMART answered without evidence, the kernel log was read and
// had no new line, nothing failed to write and the probe worked.
func (s Sample) clean() bool {
	return s.SMART != nil && len(s.SMART.evidence()) == 0 && s.Kernel != nil && s.Kernel.Errors == 0 &&
		!s.Writes.bad() && !s.ProbeFailed && len(s.Lost) == 0
}

// kernelOnly says every piece of evidence came from the kernel log.
func kernelOnly(ev []Evidence) bool {
	for _, e := range ev {
		if e.Signal != SignalKernel {
			return false
		}
	}
	return len(ev) > 0
}

// verdict is one check's result as the episodes see it.
type verdict struct {
	state      State
	kernelOnly bool // the evidence came from the kernel log only
	clean      bool // Sample.clean
}

// episodes is which conditions are open and when the disk last looked bad.
type episodes struct {
	failing, failed bool
	lastBad         time.Time
	kernelOnly      bool // the open failing episode has had kernel lines only
}

// change is one episode beginning or ending.
type change struct {
	failed bool // disk.failed; else disk.failing
	begin  bool
}

// step applies one verdict: failing begins on failing or failed and ends
// after Healthy without either, or at the first clean check when it
// rested on kernel lines only; failed lasts as long as the verdict does.
func (e episodes) step(v verdict, now time.Time) (episodes, []change) {
	bad := v.state == Failing || v.state == Failed
	if bad {
		e.lastBad = now
	}
	var out []change
	if c, ok := e.stepFailing(bad, v, now); ok {
		out = append(out, c)
	}
	if c, ok := e.stepFailed(v.state == Failed); ok {
		out = append(out, c)
	}
	return e, out
}

func (e *episodes) stepFailing(bad bool, v verdict, now time.Time) (change, bool) {
	switch {
	case bad && !e.failing:
		e.failing, e.kernelOnly = true, v.kernelOnly
		return change{begin: true}, true
	case bad:
		e.kernelOnly = e.kernelOnly && v.kernelOnly
	case e.failing && (now.Sub(e.lastBad) >= Healthy || e.kernelOnly && v.clean):
		e.failing = false
		return change{}, true
	}
	return change{}, false
}

func (e *episodes) stepFailed(failed bool) (change, bool) {
	if failed == e.failed {
		return change{}, false
	}
	e.failed = failed
	return change{failed: true, begin: failed}, true
}

// ForCloud is an episode's data as the cloud gets it: the device and each
// piece of evidence's signal and numbers, without the detail lines, which
// carry file paths and log text that stay on the hive.
func ForCloud(raw json.RawMessage) json.RawMessage {
	var d struct {
		Device   string     `json:"device"`
		Evidence []Evidence `json:"evidence"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return nil
	}
	for i := range d.Evidence {
		d.Evidence[i].Detail = ""
	}
	out, _ := json.Marshal(d) // plain strings and numbers
	return out
}
