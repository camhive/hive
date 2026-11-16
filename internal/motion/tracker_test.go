package motion_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/video"
)

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

const fps = 15

// scene plays frames at 15 fps with a keyframe every 2 s: size(sec) gives
// the P-frame size for each second. It ticks the tracker once a second, as
// the hub does, and returns every change.
func scene(tr *motion.Tracker, seconds int, size func(sec int) int) []motion.Change {
	var out []motion.Change
	for i := 0; i < seconds*fps; i++ {
		at := t0.Add(time.Duration(i) * time.Second / fps)
		f := video.Frame{At: at, Size: size(i / fps), Key: i%(2*fps) == 0}
		if f.Key {
			f.Size = 40_000
		}
		out = append(out, tr.Frame(f)...)
		if i%fps == fps-1 {
			out = append(out, tr.Tick(at)...)
		}
	}
	return out
}

func near(a, b time.Time) bool {
	d := a.Sub(b)
	return d > -1500*time.Millisecond && d < 1500*time.Millisecond
}

func TestFramesWithMovementBeginAndEndOneEvent(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 90, func(sec int) int {
		if sec >= 40 && sec < 46 {
			return 2200
		}
		return 400 + sec%3*20 // a still scene with a little noise
	})
	if len(got) != 2 {
		t.Fatalf("want begin and end, got %+v", got)
	}
	if !got[0].Begin || got[0].Source != motion.SourceFrames || !got[0].At.Equal(t0.Add(40*time.Second)) { // the first busy second, not the confirming one
		t.Fatalf("begin %+v", got[0])
	}
	if got[1].Begin || !near(got[1].At, t0.Add(46*time.Second)) {
		t.Fatalf("end %+v", got[1])
	}
}

func TestShortPausesStayOneEvent(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 90, func(sec int) int {
		if (sec >= 40 && sec < 44) || (sec >= 50 && sec < 54) { // 6 s apart: under the 10 s hold
			return 2200
		}
		return 400
	})
	if len(got) != 2 || !near(got[1].At, t0.Add(54*time.Second)) {
		t.Fatalf("want one event 40–54 s, got %+v", got)
	}
}

func TestASingleSpikeIsNotMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 60, func(sec int) int {
		if sec == 40 {
			return 3000
		}
		return 400
	})
	if len(got) != 0 {
		t.Fatalf("one busy second must not raise motion, got %+v", got)
	}
}

func TestTwoBusySecondsAreMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 60, func(sec int) int {
		if sec == 40 || sec == 41 {
			return 3000
		}
		return 400
	})
	if len(got) != 2 || !got[0].At.Equal(t0.Add(40*time.Second)) {
		t.Fatalf("two busy seconds in a row must raise motion from the first: %+v", got)
	}
}

func TestNothingIsDecidedBeforeTheBaselineIsLearnt(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 30, func(sec int) int {
		if sec < 5 {
			return 400
		}
		return 2200
	})
	for _, c := range got {
		if c.Begin && c.At.Before(t0.Add(10*time.Second)) {
			t.Fatalf("motion before warm-up: %+v", got)
		}
	}
}

func TestTheBaselineFollowsASteadyNoisierScene(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 400, func(sec int) int {
		if sec < 60 {
			return 400
		}
		return 1500 // night: the noise floor rises and stays
	})
	if len(got) != 2 || got[1].Begin || got[1].At.After(t0.Add(200*time.Second)) {
		t.Fatalf("a new steady level may look like motion for a while, then must end: %+v", got)
	}
}

func TestTinyAbsoluteChangesAreIgnored(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 60, func(sec int) int {
		if sec >= 40 && sec < 50 {
			return 180 // 3× the baseline but only 120 bytes more
		}
		return 60
	})
	if len(got) != 0 {
		t.Fatalf("a near-empty P-frame growing a little is not motion: %+v", got)
	}
}

func TestTheCamerasOwnEventsWinOverFrames(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	tr.CameraSource(true)
	var got []motion.Change
	got = append(got, scene(tr, 60, func(sec int) int {
		if sec >= 40 {
			return 3000
		}
		return 400
	})...)
	if len(got) != 0 {
		t.Fatalf("frames must be ignored while the camera reports motion itself: %+v", got)
	}
	on := t0.Add(70 * time.Second)
	got = append(got, tr.CameraMotion(true, on)...)
	for s := 71; s <= 90; s++ {
		got = append(got, tr.Tick(t0.Add(time.Duration(s)*time.Second))...)
	}
	off := t0.Add(90 * time.Second)
	got = append(got, tr.CameraMotion(false, off)...)
	for s := 91; s <= 110; s++ {
		got = append(got, tr.Tick(t0.Add(time.Duration(s)*time.Second))...)
	}
	if len(got) != 2 || !got[0].Begin || got[0].Source != motion.SourceCamera || !got[0].At.Equal(on) || got[1].Begin || !got[1].At.Equal(off) {
		t.Fatalf("want camera motion 70–90 s, got %+v", got)
	}
}

func TestLosingTheCameraSourceEndsItsMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	tr.CameraSource(true)
	got := tr.CameraMotion(true, t0)
	lost := t0.Add(5 * time.Second)
	tr.CameraSource(false)
	got = append(got, tr.CameraMotion(false, lost)...)
	for s := 6; s <= 30; s++ {
		got = append(got, tr.Tick(t0.Add(time.Duration(s)*time.Second))...)
	}
	if len(got) != 2 || got[1].Begin || !got[1].At.Equal(lost) {
		t.Fatalf("want the event to end when the source is lost, got %+v", got)
	}
}

func TestTheCamerasInitialOffReportIsNotMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	tr.CameraSource(true)
	got := tr.CameraMotion(false, t0)
	for s := 1; s <= 30; s++ {
		got = append(got, tr.Tick(t0.Add(time.Duration(s)*time.Second))...)
	}
	if len(got) != 0 {
		t.Fatalf("an off report with nothing open must change nothing: %+v", got)
	}
}

func TestExactlyTwiceTheBaselineAndTwoHundredBytesMoreIsMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 60, func(sec int) int {
		if sec >= 40 && sec < 44 {
			return 400 // 2× and +200 B over a 200 B baseline: both limits exactly
		}
		return 200
	})
	if len(got) != 2 || !got[0].At.Equal(t0.Add(40*time.Second)) {
		t.Fatalf("want motion from 40 s, got %+v", got)
	}
}

func TestASmallRiseOverABusyBaselineIsNotMotion(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 60, func(sec int) int {
		if sec >= 40 && sec < 50 {
			return 1500 // 500 B more, but only 1.5×
		}
		return 1000
	})
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestDecisionsStartRightAfterTheWarmup(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	got := scene(tr, 40, func(sec int) int {
		if sec >= 10 && sec < 15 { // ten quiet seconds learnt, then movement
			return 3000
		}
		return 400
	})
	if len(got) == 0 || !got[0].At.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("want motion from 10 s, got %+v", got)
	}
}

func TestAnEpisodeEndsExactlyOneHoldAfterItsLastActivity(t *testing.T) {
	cfg := motion.Defaults()
	tr := motion.NewTracker(cfg)
	tr.CameraSource(true)
	tr.CameraMotion(true, t0)
	tr.CameraSource(false) // the camera stops reporting: quiet from t0 on
	if got := tr.Tick(t0.Add(cfg.Hold - time.Nanosecond)); len(got) != 0 {
		t.Fatalf("ended before the hold: %+v", got)
	}
	if got := tr.Tick(t0.Add(cfg.Hold)); len(got) != 1 || got[0].Begin || !got[0].At.Equal(t0) {
		t.Fatalf("want the end at the hold, got %+v", got)
	}
}

// A camera that says "motion" and never "no motion" (a lost message, a
// stuck detector) cannot hold an episode open for ever: it ends after
// MaxEpisode, and only a fresh report starts the next.
func TestAnEpisodeIsCappedAndNeedsAFreshReportAfter(t *testing.T) {
	cfg := motion.Defaults()
	tr := motion.NewTracker(cfg)
	tr.CameraSource(true)
	got := tr.CameraMotion(true, t0)
	for s := 1; s <= 40*60; s++ {
		got = append(got, tr.Tick(t0.Add(time.Duration(s)*time.Second))...)
	}
	if len(got) != 2 || got[1].Begin || !got[1].At.Equal(t0.Add(cfg.MaxEpisode)) {
		t.Fatalf("want one episode capped at %v, got %+v", cfg.MaxEpisode, got)
	}
	if again := tr.CameraMotion(true, t0.Add(41*time.Minute)); len(again) != 1 || !again[0].Begin {
		t.Fatalf("a fresh report starts a new episode: %+v", again)
	}
}
