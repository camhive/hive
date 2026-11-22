package recordcheck

import (
	"strings"
	"testing"
	"time"
)

var w0 = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

const frame = time.Second / 15

// seg is a closed, indexed segment file starting at offset s seconds from
// w0 with d seconds of frames at 15 fps, last written at offset m.
func seg(s, d, m float64) File {
	at := func(x float64) time.Time { return w0.Add(time.Duration(x * float64(time.Second))) }
	n := int(d * 15)
	samples := make([]time.Duration, n)
	for k := range samples {
		samples[k] = frame
	}
	return File{Start: at(s), Duration: time.Duration(n) * frame, Modified: at(m), Indexed: true, Samples: samples}
}

func TestJudgeAcceptsContiguousCompleteSegments(t *testing.T) {
	// Window 0..150 s, 60 s segments; recording started 1 s before the
	// window, the last segment still being written at the window's end.
	a, b, c := seg(-1, 61, 60), seg(60, 62, 122), seg(122, 28.4, 150)
	c.Indexed = false // the newest is never closed
	if got := Judge([]File{a, b, c}, w0, w0.Add(150*time.Second), time.Minute, 15); got != "" {
		t.Fatalf("judge = %q", got)
	}
}

func TestJudgeRefuses(t *testing.T) {
	end := w0.Add(150 * time.Second)
	gap := seg(0, 60, 60)
	gap.Samples[300] = 3 * frame // two frames lost in the middle
	notIndexed := seg(-5, 60, 55)
	notIndexed.Indexed = false
	cases := map[string]struct {
		files []File
		want  string
	}{
		"nothing recorded":   {nil, "no segment"},
		"started late":       {[]File{seg(10, 140, 150)}, "started 10.0 s after"},
		"gap between":        {[]File{seg(0, 60, 60), seg(60.5, 89.5, 150)}, "0.5 s missing"},
		"short segment":      {[]File{seg(0, 30, 30), seg(30, 120, 150)}, "segment of 30.0 s"},
		"behind at the end":  {[]File{seg(0, 60, 60), seg(60, 80, 150)}, "10.0 s behind"},
		"not written lately": {[]File{seg(0, 60, 60), seg(60, 90, 140)}, "last written 10.0 s before"},
		"frames lost":        {[]File{gap, seg(60, 90, 150)}, "a frame of 200 ms"},
		"index starved":      {[]File{notIndexed, seg(55, 95, 150)}, "not in the index 95.0 s after"},
	}
	for name, tc := range cases {
		got := Judge(tc.files, w0, end, time.Minute, 15)
		if got == "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s: judge = %q, want it to mention %q", name, got, tc.want)
		}
	}
}

// A segment closed less than the index's interval ago need not be indexed
// yet.
func TestJudgeGivesTheIndexItsInterval(t *testing.T) {
	a := seg(0, 120, 120)
	a.Indexed = false
	if got := Judge([]File{a, seg(120, 30, 150)}, w0, w0.Add(150*time.Second), 2*time.Minute, 15); got != "" {
		t.Fatalf("judge = %q", got)
	}
}

// Samples whose durations add up but are too few (a stream recorded at a
// lower rate than sent) fail too.
func TestJudgeCountsFrames(t *testing.T) {
	half := seg(0, 150, 150)
	half.Samples = half.Samples[:len(half.Samples)/2]
	if got := Judge([]File{half}, w0, w0.Add(150*time.Second), 10*time.Minute, 15); !strings.Contains(got, "frames in the window") {
		t.Fatalf("judge = %q", got)
	}
}

// Files come in any order (directory listing); judge sorts them.
func TestJudgeSortsByStart(t *testing.T) {
	files := []File{seg(60, 90, 150), seg(0, 60, 60)}
	if got := Judge(files, w0, w0.Add(150*time.Second), time.Minute, 15); got != "" {
		t.Fatalf("judge = %q", got)
	}
}

// The engine's warnings inside the window fail a step; its API's answers
// and anything outside the window do not.
func TestEngineWarnings(t *testing.T) {
	from := time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	log := []byte(`2026/11/18 09:59:58 WAR [path a/main] before the window
2026/11/18 10:00:30 ERR [API] path not found
2026/11/18 10:00:31 INF [path a/main] fine
2026/11/18 10:01:00 WAR [path a/main] [recorder] too slow, discarding frames
2026/11/18 10:03:00 WAR [path a/main] after the window
`)
	got := EngineWarnings(log, from, from.Add(2*time.Minute))
	if got != "the engine logged: WAR [path a/main] [recorder] too slow, discarding frames" {
		t.Fatalf("warnings = %q", got)
	}
	if got := EngineWarnings(log[:120], from, from.Add(2*time.Minute)); got != "" {
		t.Fatalf("API answers and earlier lines count: %q", got)
	}
}

// Gaps lists the gaps between files inside the window and nothing that
// lies before the first file or after the last.
func TestGapsInsideTheWindow(t *testing.T) {
	files := []File{seg(60.2, 30, 0), seg(-30, 90, 0), seg(100, 40, 0)} // gaps of 0.2 s (tolerated) and 9.8 s
	near := func(spans []Span, want time.Duration) bool {
		var sum time.Duration
		for _, s := range spans {
			sum += s.To.Sub(s.From)
		}
		return (sum - want).Abs() < time.Millisecond
	}
	if got := Gaps(files, w0, w0.Add(150*time.Second)); len(got) != 1 || !near(got, 9800*time.Millisecond) || !got[0].To.Equal(w0.Add(100*time.Second)) {
		t.Fatalf("gaps = %v, want one of 9.8s ending at 100 s", got)
	}
	// A window that starts inside the gap keeps only its part.
	if got := Gaps(files, w0.Add(95*time.Second), w0.Add(150*time.Second)); !near(got, 5*time.Second) {
		t.Fatalf("clipped gaps = %v, want 5s", got)
	}
	if got := Gaps(nil, w0, w0.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("no files: %v", got)
	}
}

// The next segment is late once the newest is older than a segment plus
// the index's lag.
func TestLate(t *testing.T) {
	due := w0.Add(10*time.Minute + IndexLag)
	if got := Late(w0, due, 10*time.Minute); got != 0 {
		t.Fatalf("on time: %s", got)
	}
	if got := Late(w0, due.Add(time.Minute), 10*time.Minute); got != time.Minute {
		t.Fatalf("late = %s, want 1m", got)
	}
}
