// Package recordcheck judges a stream's recording against the wall clock:
// whether footage is missing between or inside segments, whether the index
// keeps up with the engine, and whether frames were lost. It is pure. The
// sizing tool (tools/sizing) judges a measured window with Judge; camera
// care (internal/health, #163) counts the footage missing and the segments
// arriving late in the last hour with Gaps and Late.
package recordcheck

import (
	"bufio"
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"
)

// IndexInterval is how often the hive reconciles its archive index with
// the files the engine wrote (cmd/hive, tools/sizing).
const IndexInterval = time.Minute

// What a recording may deviate from the wall clock and still count as
// complete and on time (docs/dev/sizing.md, "What is judged"):
const (
	// Boundary is the most footage that may be missing where one segment
	// ends and the next begins, and the most a closed segment may fall
	// short of its length: the engine cuts segments at a keyframe and
	// names them to the microsecond, so contiguous segments meet within a
	// frame (67 ms at 15 fps) plus scheduling jitter.
	Boundary = 250 * time.Millisecond
	// edge is how far the first footage may start after the window opens
	// and the last end before it closes: the engine writes 1-second parts.
	edge = time.Second
	// idle is how long before the window's end the newest file may have
	// been written last: one part plus a second for scheduling.
	idle = 2 * time.Second
	// IndexLag is how long after a segment closed the hive's index may not
	// know it yet: the index runs every IndexInterval and reads each new
	// file in full.
	IndexLag = IndexInterval + 30*time.Second
	// longSample is the longest a video sample may last, in frame
	// intervals: the source sends a frame every interval, so a longer
	// sample is where the engine or the hive lost frames.
	longSample = 1.5
)

// File is one recorded file of a stream.
type File struct {
	Start    time.Time       // from the name
	Duration time.Duration   // from the content, as the index reads it
	Bytes    int64           // size
	Modified time.Time       // last written, as seen at the window's end
	Indexed  bool            // the index had it as a closed segment at the window's end
	Samples  []time.Duration // video sample durations, in order
}

func (s File) end() time.Time { return s.Start.Add(s.Duration) }

func byStart(files []File) []File {
	return slices.SortedFunc(slices.Values(files), func(a, b File) int { return a.Start.Compare(b.Start) })
}

// Judge says what is wrong with one stream's recording of the window
// [from, to], or "" when every segment is complete and on time: recording
// began by the window's start, segments meet without missing footage,
// every closed segment is full length and was indexed in time, the
// footage reaches the window's end, the newest file was still being
// written then, and no frame of the window is missing.
func Judge(files []File, from, to time.Time, segment time.Duration, fps int) string {
	if len(files) == 0 {
		return "no segment recorded"
	}
	files = byStart(files)
	if late := files[0].Start.Sub(from); late > edge {
		return fmt.Sprintf("recording started %s after the window opened", secs(late))
	}
	for k, f := range files[:len(files)-1] {
		if why := judgeClosed(f, files[k+1], to, segment); why != "" {
			return why
		}
	}
	last := files[len(files)-1]
	if behind := to.Sub(last.end()); behind > edge {
		return fmt.Sprintf("footage %s behind at the end of the window", secs(behind))
	}
	if quiet := to.Sub(last.Modified); quiet > idle {
		return fmt.Sprintf("last written %s before the end of the window", secs(quiet))
	}
	return judgeFrames(files, from, to, fps)
}

// gap is the footage missing between f and the file after it, zero when
// they meet within Boundary.
func gap(f, next File) time.Duration {
	if g := next.Start.Sub(f.end()); g > Boundary {
		return g
	}
	return 0
}

// judgeClosed checks a closed segment against the one after it.
func judgeClosed(f, next File, to time.Time, segment time.Duration) string {
	if f.Duration < segment-Boundary {
		return fmt.Sprintf("segment of %s at %s, shorter than %s", secs(f.Duration), clock(f.Start), secs(segment))
	}
	if g := gap(f, next); g > 0 {
		return fmt.Sprintf("%s missing at %s", secs(g), clock(f.end()))
	}
	if since := to.Sub(next.Start); !f.Indexed && since > IndexLag {
		return fmt.Sprintf("segment of %s not in the index %s after it closed", clock(f.Start), secs(since))
	}
	return ""
}

// Span is a stretch of time, From inclusive, To exclusive.
type Span struct{ From, To time.Time }

// Gaps lists the footage missing between consecutive files inside
// [from, to]: every gap longer than Boundary, clipped to the window. Files
// come in any order; what lies before the first or after the last file is
// not a gap (recording had not begun, or the newest file is still open).
func Gaps(files []File, from, to time.Time) []Span {
	files = byStart(files)
	var out []Span
	for k := 1; k < len(files); k++ {
		if gap(files[k-1], files[k]) == 0 {
			continue
		}
		a, b := maxTime(files[k-1].end(), from), minTime(files[k].Start, to)
		if b.After(a) {
			out = append(out, Span{From: a, To: b})
		}
	}
	return out
}

// Late is how long past due the next segment is at now, zero when it is
// not: the engine starts a new file every segment, and the index knows it
// within IndexLag, so newest, the start of the newest file the index has,
// may be at most segment + IndexLag old.
func Late(newest, now time.Time, segment time.Duration) time.Duration {
	if d := now.Sub(newest) - segment - IndexLag; d > 0 {
		return d
	}
	return 0
}

// judgeFrames checks every video sample that starts inside the window: none
// lasts longer than longSample frame intervals, and there are as many as
// the window holds at fps.
func judgeFrames(files []File, from, to time.Time, fps int) string {
	interval := time.Second / time.Duration(fps)
	limit := time.Duration(longSample * float64(interval))
	count := 0
	for _, f := range files {
		at := f.Start
		for _, d := range f.Samples {
			if !at.Before(from) && at.Before(to) {
				count++
				if d > limit {
					return fmt.Sprintf("a frame of %s at %s: frames lost", millis(d), clock(at))
				}
			}
			at = at.Add(d)
		}
	}
	if want := int(to.Sub(from) / interval); count < want-fps/2 {
		return fmt.Sprintf("%d frames in the window, %d expected", count, want)
	}
	return ""
}

// EngineWarnings returns the first warning or error the engine logged in
// [from, to], or "". Lines from its control API ("[API]") answer the
// hive's own queries (a path asked for before it exists) and say nothing
// about recording.
func EngineWarnings(log []byte, from, to time.Time) string {
	sc := bufio.NewScanner(bytes.NewReader(log))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 23 {
			continue
		}
		at, err := time.ParseInLocation("2006/01/02 15:04:05", line[:19], time.Local)
		level := line[20:23]
		if err != nil || (level != "WAR" && level != "ERR") || strings.Contains(line, "[API]") {
			continue
		}
		if !at.Before(from.Truncate(time.Second)) && !at.After(to) {
			return "the engine logged: " + strings.TrimSpace(line[20:])
		}
	}
	return ""
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func secs(d time.Duration) string { return fmt.Sprintf("%.1f s", d.Seconds()) }
func millis(d time.Duration) string {
	return fmt.Sprintf("%d ms", d.Round(time.Millisecond).Milliseconds())
}
func clock(t time.Time) string { return t.UTC().Format("15:04:05") }
