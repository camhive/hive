package health_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// noon on a weekday, local time of the tests (UTC keeps hours readable).
var noon = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// world is what care reads, kept by the test: the index's segments, the
// timeline, the camera's clock, and what care said.
type world struct {
	mu        sync.Mutex
	now       time.Time
	segs      map[string][]store.SegmentSize // camera/stream
	events    []store.Event
	drift     time.Duration // the camera's clock minus the hive's
	clockErr  error
	clockAsks int
	degraded  [][]health.Reason // the reasons at each episode's first call
	since     []time.Time       // each episode's start
	recovered int
	ended     int // episodes ended because the camera was disabled or removed
}

func newWorld(at time.Time) *world { return &world{now: at, segs: map[string][]store.SegmentSize{}} }

// onDegraded records an episode on its first call (care calls on every
// check while degraded).
func (w *world) onDegraded(_ string, since time.Time, r []health.Reason) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n := len(w.since); n == 0 || !w.since[n-1].Equal(since) {
		w.since = append(w.since, since)
		w.degraded = append(w.degraded, r)
	}
}

func (w *world) onRecovered(_ string, _ time.Time, recovered bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if recovered {
		w.recovered++
	} else {
		w.ended++
	}
}

func (w *world) Now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *world) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

func (w *world) Sizes(_ context.Context, cam, stream string, from, to time.Time) ([]store.SegmentSize, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []store.SegmentSize
	for _, r := range w.segs[cam+"/"+stream] {
		if !r.Start.Before(from) && r.Start.Before(to) {
			out = append(out, r)
		}
	}
	return out, nil
}

// events implements care's timeline source.
type events struct{ w *world }

func (e events) Range(_ context.Context, cam string, from, to time.Time) ([]store.Event, error) {
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	var out []store.Event
	for _, ev := range e.w.events {
		if (ev.Camera == cam || ev.Camera == "") && ev.At.Before(to) && !ev.At.Before(from) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (w *world) event(cam, kind string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, store.Event{Camera: cam, Kind: kind, At: at, End: at})
}

func (w *world) clock(context.Context, camera.Camera) (time.Time, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clockAsks++
	return w.now.Add(w.drift), w.clockErr
}

// record adds closed segments of seg length at kbps covering [from, to).
func (w *world) record(cam, stream string, from, to time.Time, kbps int, seg time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for at := from; at.Before(to); at = at.Add(seg) {
		d := min(seg, to.Sub(at))
		w.segs[cam+"/"+stream] = append(w.segs[cam+"/"+stream], store.SegmentSize{Start: at, Duration: d, Bytes: int64(kbps) * 1000 / 8 * int64(d/time.Second)})
	}
}

// profile records 10-minute segments over [from, to) at the bitrate the
// local hour asks for.
func (w *world) profile(cam, stream string, from, to time.Time, kbps func(hour int) int) {
	for at := from; at.Before(to); at = at.Add(10 * time.Minute) {
		w.record(cam, stream, at, earliest(at.Add(10*time.Minute), to), kbps(at.Hour()), 10*time.Minute)
	}
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func flat(kbps int) func(int) int { return func(int) int { return kbps } }

func (w *world) reasons() [][]health.Reason {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.degraded)
}

func (w *world) recoveries() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recovered
}

// longAgo is when the hive of a test started: long before what it judges.
var longAgo = noon.AddDate(0, -1, 0)

func newCare(t *testing.T, w *world, cams ...camera.Camera) *health.Care {
	t.Helper()
	return newCareStarted(t, w, longAgo, cams...)
}

func newCareStarted(t *testing.T, w *world, started time.Time, cams ...camera.Camera) *health.Care {
	t.Helper()
	return health.NewCare(health.CareSources{Cameras: &memStore{cams: cams}, Segments: w, Events: events{w}, Clock: w.clock},
		health.CareOptions{Now: w.Now, Started: started, Location: time.UTC, Segment: 10 * time.Minute, OnDegraded: w.onDegraded, OnRecovered: w.onRecovered})
}

func check(t *testing.T, c *health.Care) {
	t.Helper()
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func reasonsOf(cc health.CameraCare) []health.Reason {
	out := make([]health.Reason, 0, len(cc.Reasons))
	for _, r := range cc.Reasons {
		out = append(out, r.Reason)
	}
	return out
}

// A stream that sends half its usual bitrate for an hour degrades the
// Camera, once per episode; back to normal, it recovers.
func TestCareRaisesBitrateDropOnceAndRecovers(t *testing.T) {
	c0 := cam(t, "front")
	id := string(c0.ID)
	w := newWorld(noon)
	w.profile(id, "main", noon.AddDate(0, 0, -3), noon, flat(4000))
	w.record(id, "main", noon, noon.Add(time.Hour), 2000, 10*time.Minute)
	c := newCare(t, w, c0)
	check(t, c)
	if got := c.Camera(id); got.Degraded {
		t.Fatalf("degraded before the hour: %+v", got)
	}
	w.advance(time.Hour)
	check(t, c)
	w.advance(5 * time.Minute)
	check(t, c) // still the same episode
	got := c.Camera(id)
	if !got.Degraded || !slices.Contains(reasonsOf(got), health.BitrateDrop) || len(w.reasons()) != 1 {
		t.Fatalf("care = %+v; episodes %v", got, w.reasons())
	}
	main := got.Signals.Streams[0]
	if main.Stream != "main" || main.UsualKbps != 4000 || main.LastHourKbps > 2100 || main.LastHourKbps < 1900 {
		t.Fatalf("main stream signal %+v", main)
	}
	if !strings.Contains(got.Reasons[0].Suggestion, "quieter scene") || got.Since == nil || got.Thresholds.BitrateDrop == 0 {
		t.Fatalf("view lacks suggestion, since or thresholds: %+v", got)
	}
	w.record(id, "main", noon.Add(time.Hour), noon.Add(2*time.Hour+5*time.Minute), 4000, 10*time.Minute)
	w.advance(time.Hour)
	check(t, c)
	if got := c.Camera(id); got.Degraded || w.recoveries() != 1 {
		t.Fatalf("not recovered: %+v, recoveries %d", got, w.recoveries())
	}
}

// Without several days of the same hour there is no usual bitrate, and
// no judgement.
func TestCareWaitsForDaysOfHistory(t *testing.T) {
	c0 := cam(t, "front")
	id := string(c0.ID)
	w := newWorld(noon)
	w.profile(id, "main", noon.AddDate(0, 0, -2), noon.Add(-time.Hour), flat(4000)) // two days
	w.record(id, "main", noon.Add(-time.Hour), noon, 1000, 10*time.Minute)
	c := newCare(t, w, c0)
	check(t, c)
	if got := c.Camera(id); got.Degraded || got.Signals.Streams[0].UsualKbps != 0 {
		t.Fatalf("care = %+v", got)
	}
}

// A camera whose scene is quieter every evening (VBR) sends less at that
// hour every day: normal, no reason.
func TestCareAcceptsADailyEveningDrop(t *testing.T) {
	c0 := cam(t, "front")
	id := string(c0.ID)
	evening := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	w := newWorld(evening)
	vbr := func(h int) int {
		if h >= 17 {
			return 1500
		}
		return 4000
	}
	w.profile(id, "main", evening.AddDate(0, 0, -4), evening, vbr)
	c := newCare(t, w, c0)
	check(t, c)
	if got := c.Camera(id); got.Degraded || got.Signals.Streams[0].UsualKbps != 1500 {
		t.Fatalf("care = %+v", got)
	}
}

// Every stream is watched: a sub stream that drops is a reason too.
func TestCareWatchesTheSubStream(t *testing.T) {
	c0 := cam(t, "front")
	id := string(c0.ID)
	w := newWorld(noon)
	w.profile(id, "main", noon.AddDate(0, 0, -3), noon, flat(4000))
	w.profile(id, "sub", noon.AddDate(0, 0, -3), noon.Add(-time.Hour), flat(500))
	w.record(id, "sub", noon.Add(-time.Hour), noon, 100, 10*time.Minute)
	c := newCare(t, w, c0)
	check(t, c)
	got := c.Camera(id)
	if !slices.Equal(reasonsOf(got), []health.Reason{health.BitrateDrop}) || len(got.Signals.Streams) != 2 ||
		got.Signals.Streams[0].Stream != "main" || got.Signals.Streams[1].LastHourKbps != 100 {
		t.Fatalf("care = %+v", got)
	}
}

// dayAndNight is a camera with 4000 kbit/s by day and `nightKbps` at night.
func dayAndNight(nightKbps int) func(int) int {
	return func(h int) int {
		if h >= 20 || h < 8 {
			return nightKbps
		}
		return 4000
	}
}

// A night hour that fell against the camera's own earlier nights and lies
// far below its day says the IR failed; it stands for the drop too, and
// clears by day.
func TestCareSeesTheNightPictureCollapse(t *testing.T) {
	c0 := cam(t, "yard")
	id := string(c0.ID)
	night := time.Date(2026, 11, 18, 23, 0, 0, 0, time.UTC)
	w := newWorld(night)
	w.profile(id, "main", night.AddDate(0, 0, -4), night.Add(-time.Hour), dayAndNight(2000))
	w.record(id, "main", night.Add(-time.Hour), night, 400, 10*time.Minute)
	c := newCare(t, w, c0)
	check(t, c)
	got := c.Camera(id)
	st := got.Signals.Streams[0]
	if !slices.Equal(reasonsOf(got), []health.Reason{health.NightPicture}) || !st.Night || st.DayKbps != 4000 || st.UsualKbps != 2000 {
		t.Fatalf("care = %+v", got)
	}
	if got.Reasons[0].Suggestion != "The IR illuminator may have failed." {
		t.Fatalf("suggestion %q", got.Reasons[0].Suggestion)
	}
	w.profile(id, "main", night, night.Add(10*time.Hour), dayAndNight(400)) // until 09:00, the day picture is fine
	w.advance(10 * time.Hour)
	check(t, c)
	if got := c.Camera(id); got.Degraded {
		t.Fatalf("by day: %+v", got)
	}
}

// A camera whose nights are dark by design (every night far below the
// day) has no night picture problem.
func TestCareAcceptsNightsThatAreAlwaysDark(t *testing.T) {
	c0 := cam(t, "yard")
	id := string(c0.ID)
	night := time.Date(2026, 11, 18, 23, 0, 0, 0, time.UTC)
	w := newWorld(night)
	w.profile(id, "main", night.AddDate(0, 0, -4), night, dayAndNight(300))
	c := newCare(t, w, c0)
	check(t, c)
	if got := c.Camera(id); got.Degraded {
		t.Fatalf("care = %+v", got)
	}
}

// Footage missing between segments degrades the Camera; time it was
// offline does not count (offline is its own notification).
func TestCareCountsMissingFootageButNotOfflineTime(t *testing.T) {
	c0 := cam(t, "door")
	id := string(c0.ID)
	w := newWorld(noon)
	for k := range 6 { // 9m30s of every 10 minutes: 3 minutes missing in the hour
		at := noon.Add(-time.Hour + time.Duration(k)*10*time.Minute)
		w.record(id, "main", at, at.Add(9*time.Minute+30*time.Second), 4000, 10*time.Minute)
	}
	w.record(id, "main", noon, noon, 0, time.Minute) // the open segment
	c := newCare(t, w, c0)
	check(t, c)
	got := c.Camera(id)
	if !slices.Contains(reasonsOf(got), health.FrameLoss) || got.Signals.MissingS < 140 || got.Signals.MissingS > 151 {
		t.Fatalf("care = %+v", got)
	}

	w2 := newWorld(noon)
	w2.record(id, "main", noon.Add(-time.Hour), noon.Add(-40*time.Minute), 4000, 10*time.Minute)
	w2.record(id, "main", noon.Add(-30*time.Minute), noon, 4000, 10*time.Minute)
	w2.event(id, "camera.offline", noon.Add(-40*time.Minute))
	w2.event(id, "camera.online", noon.Add(-30*time.Minute))
	c2 := newCare(t, w2, c0)
	check(t, c2)
	if got := c2.Camera(id); slices.Contains(reasonsOf(got), health.FrameLoss) || got.Signals.MissingS != 0 {
		t.Fatalf("offline time counted as missing: %+v", got)
	}
}

// Segments that stop arriving while the camera is online are late, once
// three checks in a row found them late; not while the storage is low.
func TestCareSeesLateSegments(t *testing.T) {
	c0 := cam(t, "door")
	id := string(c0.ID)
	for name, low := range map[string]bool{"storage fine": false, "storage low": true} {
		w := newWorld(noon)
		w.record(id, "main", noon.Add(-2*time.Hour), noon.Add(-30*time.Minute), 4000, 10*time.Minute)
		if low {
			w.mu.Lock()
			w.events = append(w.events, store.Event{Kind: "storage.low", At: noon.Add(-time.Hour)}) // still going on
			w.mu.Unlock()
		}
		c := newCare(t, w, c0)
		c.Observe(health.Sample{Camera: id, Stream: "main", At: noon, Ready: true, Status: video.Status{Ready: true, BytesReceived: 1}})
		for k := range 3 {
			check(t, c)
			if got := c.Camera(id); k < 2 && got.Degraded {
				t.Fatalf("%s: late after %d checks: %+v", name, k+1, got)
			}
			w.advance(5 * time.Minute)
		}
		got := c.Camera(id)
		if low == slices.Contains(reasonsOf(got), health.FrameLoss) || got.Signals.LateS < 60 {
			t.Fatalf("%s: care = %+v", name, got)
		}
	}
}

// The gap of a hive restart (a reboot, an update) is not missing footage:
// nothing could record.
func TestCareDoesNotCountTheHivesOwnDowntime(t *testing.T) {
	c0 := cam(t, "door")
	id := string(c0.ID)
	w := newWorld(noon)
	w.record(id, "main", noon.Add(-2*time.Hour), noon.Add(-20*time.Minute), 4000, 10*time.Minute)
	w.record(id, "main", noon.Add(-18*time.Minute), noon, 4000, 10*time.Minute)
	c := newCareStarted(t, w, noon.Add(-18*time.Minute-30*time.Second), c0)
	check(t, c)
	if got := c.Camera(id); got.Degraded || got.Signals.MissingS != 0 {
		t.Fatalf("after the hive's restart: %+v", got)
	}
	// The engine restarting on its own is the same.
	w2 := newWorld(noon)
	w2.record(id, "main", noon.Add(-2*time.Hour), noon.Add(-20*time.Minute), 4000, 10*time.Minute)
	w2.record(id, "main", noon.Add(-18*time.Minute), noon, 4000, 10*time.Minute)
	c2 := newCare(t, w2, c0)
	c2.EngineStarted(noon.Add(-18*time.Minute - 30*time.Second))
	check(t, c2)
	if got := c2.Camera(id); got.Degraded || got.Signals.MissingS != 0 {
		t.Fatalf("after the engine's restart: %+v", got)
	}
}

// poll feeds care one poll of a camera's main stream.
func poll(c *health.Care, id string, at time.Time, ready bool, bytes int64) {
	c.Observe(health.Sample{Camera: id, Stream: "main", At: at, Ready: ready,
		Status: video.Status{Ready: ready, BytesReceived: bytes, Video: video.Track{Codec: "H264", Width: 1920, Height: 1080}}})
}

// A stream that drops and comes back, or whose byte counter starts again
// (the engine reconnected between two polls), reconnected.
func TestCareCountsReconnects(t *testing.T) {
	c0 := cam(t, "gate")
	id := string(c0.ID)
	w := newWorld(noon)
	c := newCare(t, w, c0)
	at := noon.Add(-time.Hour)
	poll(c, id, at, true, 100)
	for k := range 3 { // three drops seen by a poll
		at = at.Add(5 * time.Minute)
		poll(c, id, at, false, 0)
		at = at.Add(5 * time.Second)
		poll(c, id, at, true, int64(10+k))
	}
	at = at.Add(5 * time.Minute)
	poll(c, id, at, true, 1) // counter started again: a fourth
	poll(c, id, at.Add(5*time.Second), true, 5000)
	check(t, c)
	got := c.Camera(id)
	if got.Signals.ReconnectsHour != 4 || got.Signals.Reconnects24h != 4 || !slices.Contains(reasonsOf(got), health.Reconnects) {
		t.Fatalf("care = %+v", got)
	}
	w.advance(2 * time.Hour)
	check(t, c)
	if got := c.Camera(id); got.Signals.ReconnectsHour != 0 || got.Signals.Reconnects24h != 4 || got.Degraded {
		t.Fatalf("an hour later: %+v", got)
	}
}

// The camera's clock is read every six hours; off by more than 30 s, it
// is a reason.
func TestCareChecksTheCameraClockEverySixHours(t *testing.T) {
	c0 := cam(t, "porch")
	c0.Source.Kind = camera.SourceONVIF
	id := string(c0.ID)
	w := newWorld(noon)
	w.drift = 45 * time.Second
	c := newCare(t, w, c0)
	check(t, c)
	got := c.Camera(id)
	if got.Signals.ClockDriftS == nil || *got.Signals.ClockDriftS != 45 || !slices.Contains(reasonsOf(got), health.ClockDrift) {
		t.Fatalf("care = %+v", got)
	}
	w.drift = 0
	w.advance(time.Hour)
	check(t, c)
	if w.clockAsks != 1 || !c.Camera(id).Degraded {
		t.Fatalf("asked %d times within six hours", w.clockAsks)
	}
	w.advance(5 * time.Hour)
	check(t, c)
	if got := c.Camera(id); w.clockAsks != 2 || got.Degraded || *got.Signals.ClockDriftS != 0 {
		t.Fatalf("after six hours: asks %d, %+v", w.clockAsks, got)
	}
}

// A camera reached by a bare RTSP URL has no clock to read.
func TestCareLeavesRTSPCamerasClocksAlone(t *testing.T) {
	c0 := cam(t, "porch")
	w := newWorld(noon)
	c := newCare(t, w, c0)
	check(t, c)
	if w.clockAsks != 0 || c.Camera(string(c0.ID)).Signals.ClockDriftS != nil {
		t.Fatal("asked an RTSP camera for its time")
	}
}

// Offline at the same hour on three of the last seven days is a pattern;
// the minutes offline per day are kept for the line.
func TestCareFindsAnOfflinePattern(t *testing.T) {
	c0 := cam(t, "shed")
	id := string(c0.ID)
	w := newWorld(noon)
	for _, d := range []int{1, 2, 4} {
		at := time.Date(2026, 11, 18-d, 3, 10, 0, 0, time.UTC)
		w.event(id, "camera.offline", at)
		w.event(id, "camera.online", at.Add(20*time.Minute))
	}
	w.event(id, "camera.offline", noon.Add(-6*time.Hour)) // once at 06:00: no pattern
	w.event(id, "camera.online", noon.Add(-6*time.Hour+10*time.Minute))
	c := newCare(t, w, c0)
	check(t, c)
	got := c.Camera(id)
	if !slices.Contains(reasonsOf(got), health.OfflinePattern) || !slices.Equal(got.Signals.PatternHours, []int{3}) {
		t.Fatalf("care = %+v", got)
	}
	days := got.Signals.OfflineMinutes
	if len(days) != 7 || days[6].Day != "2026-11-18" || days[6].Minutes != 10 || days[5].Minutes != 20 || days[4].Minutes != 20 || days[3].Minutes != 0 {
		t.Fatalf("offline minutes %+v", days)
	}
}

// A camera that was offline for whole days has no pattern, only an outage.
func TestCareSeesNoPatternInALongOutage(t *testing.T) {
	c0 := cam(t, "shed")
	id := string(c0.ID)
	w := newWorld(noon)
	w.event(id, "camera.offline", noon.Add(-4*24*time.Hour))
	w.event(id, "camera.online", noon.Add(-time.Hour))
	c := newCare(t, w, c0)
	check(t, c)
	if got := c.Camera(id); slices.Contains(reasonsOf(got), health.OfflinePattern) {
		t.Fatalf("care = %+v", got)
	}
}

// A stream that changes codec or resolution on its own is a reason for a
// day; one the owner changed (the Camera was updated) is not.
func TestCareNoticesAStreamChangeWithoutTheOwner(t *testing.T) {
	c0 := cam(t, "hall")
	c0.UpdatedAt = noon.Add(-48 * time.Hour)
	id := string(c0.ID)
	w := newWorld(noon)
	c := newCare(t, w, c0)
	hd := video.Track{Codec: "H264", Width: 1920, Height: 1080}
	c.Observe(health.Sample{Camera: id, Stream: "main", At: noon, Ready: true, Status: video.Status{Ready: true, BytesReceived: 1, Video: hd}})
	c.Observe(health.Sample{Camera: id, Stream: "main", At: noon.Add(time.Minute), Ready: true,
		Status: video.Status{Ready: true, BytesReceived: 2, Video: video.Track{Codec: "H264", Width: 640, Height: 360}}})
	check(t, c)
	got := c.Camera(id)
	ch := got.Signals.StreamChange
	if !slices.Contains(reasonsOf(got), health.StreamChanged) || ch == nil || ch.From != hd || ch.To.Width != 640 {
		t.Fatalf("care = %+v", got)
	}
	w.advance(25 * time.Hour)
	check(t, c)
	if got := c.Camera(id); got.Degraded {
		t.Fatalf("a day later: %+v", got)
	}

	owned := cam(t, "attic")
	owned.UpdatedAt = noon
	w2 := newWorld(noon.Add(2 * time.Minute))
	c2 := newCare(t, w2, owned)
	oid := string(owned.ID)
	c2.Observe(health.Sample{Camera: oid, Stream: "main", At: noon, Ready: true, Status: video.Status{Ready: true, BytesReceived: 1, Video: hd}})
	c2.Observe(health.Sample{Camera: oid, Stream: "main", At: noon.Add(time.Minute), Ready: true,
		Status: video.Status{Ready: true, BytesReceived: 2, Video: video.Track{Codec: "H265", Width: 1920, Height: 1080}}})
	check(t, c2)
	if got := c2.Camera(oid); got.Degraded {
		t.Fatalf("the owner's change reported: %+v", got)
	}
}

// A disabled Camera is not watched; its episode ends without a recovery
// (the camera is not well again, the owner switched it off).
func TestCareEndsTheEpisodeOfADisabledCameraSilently(t *testing.T) {
	c0 := cam(t, "porch")
	c0.Source.Kind = camera.SourceONVIF
	w := newWorld(noon)
	w.drift = time.Minute
	st := &memStore{cams: []camera.Camera{c0}}
	c := health.NewCare(health.CareSources{Cameras: st, Segments: w, Events: events{w}, Clock: w.clock},
		health.CareOptions{Now: w.Now, Started: longAgo, Location: time.UTC, OnDegraded: w.onDegraded, OnRecovered: w.onRecovered})
	check(t, c)
	if !c.Camera(string(c0.ID)).Degraded {
		t.Fatal("not degraded")
	}
	st.mu.Lock()
	st.cams[0].Enabled = false
	st.mu.Unlock()
	check(t, c)
	if c.Camera(string(c0.ID)).Degraded || w.recovered != 0 || w.ended != 1 {
		t.Fatalf("still degraded; recoveries %d, silent ends %d", w.recovered, w.ended)
	}
}

// Only real stream drops are reconnects: not a poll the engine did not
// answer, not the streams coming up after the engine restarted, not the
// source restarting right after the owner changed the camera.
func TestCareCountsOnlyRealDrops(t *testing.T) {
	c0 := cam(t, "gate")
	c0.UpdatedAt = noon.Add(-10 * time.Minute)
	id := string(c0.ID)
	w := newWorld(noon)
	c := newCare(t, w, c0)
	at := noon.Add(-50 * time.Minute)
	poll(c, id, at, true, 100)
	c.Observe(health.Sample{Camera: id, Stream: "main", At: at.Add(5 * time.Second), Err: errors.New("engine API down")})
	poll(c, id, at.Add(10*time.Second), true, 200)
	c.EngineStarted(at.Add(20 * time.Minute))
	poll(c, id, at.Add(20*time.Minute+10*time.Second), true, 5) // a fresh engine counts from zero
	poll(c, id, noon.Add(-10*time.Minute+5*time.Second), false, 0)
	poll(c, id, noon.Add(-10*time.Minute+30*time.Second), true, 1) // the owner's edit re-added the source
	check(t, c)
	if got := c.Camera(id); got.Signals.Reconnects24h != 0 {
		t.Fatalf("care = %+v", got)
	}
	poll(c, id, noon.Add(-time.Minute), false, 0)
	poll(c, id, noon.Add(-time.Minute+5*time.Second), true, 1)
	check(t, c)
	if got := c.Camera(id); got.Signals.Reconnects24h != 1 {
		t.Fatalf("a real drop: %+v", got)
	}
}

// A camera that did not answer for its clock is asked again after half an
// hour, not after six.
func TestCareRetriesAFailedClockReadingSooner(t *testing.T) {
	c0 := cam(t, "porch")
	c0.Source.Kind = camera.SourceONVIF
	w := newWorld(noon)
	w.clockErr = errors.New("timeout")
	c := newCare(t, w, c0)
	check(t, c)
	w.advance(25 * time.Minute)
	check(t, c)
	w.clockErr = nil
	w.advance(5 * time.Minute)
	check(t, c)
	w.advance(time.Hour)
	check(t, c)
	if w.clockAsks != 2 || c.Camera(string(c0.ID)).Signals.ClockDriftS == nil {
		t.Fatalf("asked %d times", w.clockAsks)
	}
}
