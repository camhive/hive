package motion_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/motion"
)

// movement plays a still scene of 400 B P-frames with `size` from second
// 40 for `secs` seconds and returns the episodes' beginnings.
func movement(level motion.Level, size, secs int) []motion.Change {
	tr := motion.NewTracker(motion.Defaults())
	tr.SetLevel(level)
	var begins []motion.Change
	for _, c := range scene(tr, 90, func(sec int) int {
		if sec >= 40 && sec < 40+secs {
			return size
		}
		return 400
	}) {
		if c.Begin {
			begins = append(begins, c)
		}
	}
	return begins
}

// #157: the sensitivity moves the threshold (a multiple of the baseline).
func TestSensitivityMovesTheThreshold(t *testing.T) {
	cases := []struct {
		s    camera.Sensitivity
		size int // P-frame bytes over a 400 B baseline, for 8 s
		want int
	}{
		{camera.SensitivityHigh, 700, 1},   // 1.75×
		{camera.SensitivityNormal, 700, 0}, // under 2×
		{camera.SensitivityNormal, 1000, 1},
		{camera.SensitivityLow, 1000, 0}, // under 3×
		{camera.SensitivityLow, 1400, 1},
	}
	for _, c := range cases {
		if got := movement(motion.LevelFor(c.s, false), c.size, 8); len(got) != c.want {
			t.Errorf("%v at %d B: %d episodes, want %d", c.s, c.size, len(got), c.want)
		}
	}
}

// #157: and the minimum episode length: low needs four busy seconds.
func TestLowSensitivityIgnoresShortMovement(t *testing.T) {
	if got := movement(motion.LevelFor(camera.SensitivityLow, false), 3000, 3); len(got) != 0 {
		t.Fatalf("3 s at low: %+v", got)
	}
	got := movement(motion.LevelFor(camera.SensitivityLow, false), 3000, 5)
	if len(got) != 1 || !got[0].At.Equal(t0.Add(40*time.Second)) {
		t.Fatalf("5 s at low: %+v", got)
	}
	if got := movement(motion.LevelFor(camera.SensitivityNormal, false), 3000, 3); len(got) != 1 {
		t.Fatalf("3 s at normal: %+v", got)
	}
}

// Calming raises the threshold one step; low calmed is stricter than low.
func TestCalmingIsOneStepLessSensitive(t *testing.T) {
	frames := func(l motion.Level) [2]float64 { return [2]float64{l.Ratio, float64(l.Confirm)} }
	if frames(motion.LevelFor(camera.SensitivityHigh, true)) != frames(motion.LevelFor(camera.SensitivityNormal, false)) ||
		frames(motion.LevelFor(camera.SensitivityNormal, true)) != frames(motion.LevelFor(camera.SensitivityLow, false)) {
		t.Fatal("calmed is not the next level")
	}
	if l, low := motion.LevelFor(camera.SensitivityLow, true), motion.LevelFor(camera.SensitivityLow, false); l.Ratio <= low.Ratio || l.Confirm <= low.Confirm {
		t.Fatalf("low calmed %+v, low %+v", l, low)
	}
	if motion.LevelFor(camera.SensitivityNormal, false) != motion.Defaults().Level {
		t.Fatal("normal is not ADR-0020's defaults")
	}
}

// The camera's own detector: at low a report must last its minimum length
// before an episode begins, at its start; normal begins at once.
func TestLowSensitivityNeedsALastingCameraReport(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	tr.SetLevel(motion.LevelFor(camera.SensitivityLow, false))
	tr.CameraSource(true)
	if got := tr.CameraMotion(true, t0); len(got) != 0 {
		t.Fatalf("begun at once: %+v", got)
	}
	if got := append(tr.Tick(t0.Add(time.Second)), tr.CameraMotion(false, t0.Add(2*time.Second))...); len(got) != 0 {
		t.Fatalf("a 2 s report began an episode: %+v", got)
	}
	if got := tr.Tick(t0.Add(20 * time.Second)); len(got) != 0 {
		t.Fatalf("after the short report: %+v", got)
	}
	start := t0.Add(30 * time.Second)
	tr.CameraMotion(true, start)
	var got []motion.Change
	for s := 1; s <= 5; s++ {
		got = append(got, tr.Tick(start.Add(time.Duration(s)*time.Second))...)
	}
	if len(got) != 1 || !got[0].Begin || !got[0].At.Equal(start) || got[0].Source != motion.SourceCamera {
		t.Fatalf("lasting report: %+v", got)
	}

	normal := motion.NewTracker(motion.Defaults())
	normal.CameraSource(true)
	if got := normal.CameraMotion(true, t0); len(got) != 1 {
		t.Fatalf("normal: %+v", got)
	}
}

// #157 review: calming means something for the camera's own detector at
// every sensitivity; at high it wants a report of a second.
func TestACalmedCameraSourceWantsALongerReport(t *testing.T) {
	for _, s := range []camera.Sensitivity{camera.SensitivityHigh, camera.SensitivityNormal, camera.SensitivityLow} {
		if calmed, plain := motion.LevelFor(s, true), motion.LevelFor(s, false); calmed.CameraMin <= plain.CameraMin {
			t.Errorf("%v: calmed %v, plain %v", s, calmed.CameraMin, plain.CameraMin)
		}
	}
	if l := motion.LevelFor(camera.SensitivityHigh, true); l.CameraMin != time.Second {
		t.Fatalf("high calmed: %v", l.CameraMin)
	}
}

// Short pulses less than a second apart are one lasting report: a camera
// that flickers on and off still reaches low's three seconds.
func TestPulsesASecondApartAreOneReport(t *testing.T) {
	tr := motion.NewTracker(motion.Defaults())
	tr.SetLevel(motion.LevelFor(camera.SensitivityLow, false))
	tr.CameraSource(true)
	var got []motion.Change
	for i := range 10 { // on for 200 ms every 500 ms, for 5 s
		at := t0.Add(time.Duration(i) * 500 * time.Millisecond)
		got = append(got, tr.CameraMotion(true, at)...)
		got = append(got, tr.CameraMotion(false, at.Add(200*time.Millisecond))...)
		if i%2 == 1 {
			got = append(got, tr.Tick(at.Add(300*time.Millisecond))...)
		}
	}
	if len(got) != 1 || !got[0].Begin || !got[0].At.Equal(t0) {
		t.Fatalf("pulses: %+v", got)
	}
	// Pulses two seconds apart are separate short reports: nothing begins.
	tr = motion.NewTracker(motion.Defaults())
	tr.SetLevel(motion.LevelFor(camera.SensitivityLow, false))
	tr.CameraSource(true)
	got = make([]motion.Change, 0, 1)
	for i := range 5 {
		at := t0.Add(time.Duration(i) * 2 * time.Second)
		got = append(got, tr.CameraMotion(true, at)...)
		got = append(got, tr.CameraMotion(false, at.Add(200*time.Millisecond))...)
		got = append(got, tr.Tick(at.Add(time.Second))...)
	}
	if len(got) != 0 {
		t.Fatalf("sparse pulses: %+v", got)
	}
}
