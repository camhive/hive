package health

import (
	"slices"
	"sort"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/recordcheck"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// Signals are care's measurements of one Camera.
type Signals struct {
	Streams        []StreamSignal `json:"streams"`
	MissingS       float64        `json:"missing_s"`        // footage missing in the last hour, offline time excluded
	LateS          float64        `json:"late_s"`           // how long the next segment is overdue
	ReconnectsHour int            `json:"reconnects_hour"`  // in the last hour
	Reconnects24h  int            `json:"reconnects_24h"`   // in the last 24 hours, since the hive started
	ClockDriftS    *float64       `json:"clock_drift_s"`    // the camera's clock minus the hive's; nil: not read
	ClockCheckedAt *time.Time     `json:"clock_checked_at"` // nil: not read
	OfflineMinutes []DayMinutes   `json:"offline_minutes"`  // per local day, the last 7, today last
	PatternHours   []int          `json:"pattern_hours"`    // local hours the Camera keeps dropping in
	StreamChange   *StreamChange  `json:"stream_change"`    // nil: none to report
}

// StreamSignal is one stream's bitrate.
type StreamSignal struct {
	Stream       string `json:"stream"`
	LastHourKbps int64  `json:"last_hour_kbps"` // 0: not enough footage in the last hour
	UsualKbps    int64  `json:"usual_kbps"`     // median of the same part of the day; 0: not known yet
	DayKbps      int64  `json:"day_kbps"`       // median by day; 0: not known yet
	Night        bool   `json:"night"`          // the last hour is night
}

// DayMinutes is the time a Camera was offline on one local day.
type DayMinutes struct {
	Day     string `json:"day"` // 2006-01-02
	Minutes int    `json:"minutes"`
}

// StreamChange is a codec or resolution change the engine saw.
type StreamChange struct {
	Stream string      `json:"stream"`
	At     time.Time   `json:"at"`
	From   video.Track `json:"from"`
	To     video.Track `json:"to"`
}

func emptySignals() Signals {
	return Signals{Streams: []StreamSignal{}, OfflineMinutes: []DayMinutes{}, PatternHours: []int{}}
}

// careInput is everything assess looks at.
type careInput struct {
	now        time.Time
	loc        *time.Location
	segment    time.Duration
	updated    time.Time // the Camera's last change by its owner
	online     bool      // the main stream's last poll was ready
	restarted  time.Time // the hive or its engine last started: footage before it is not judged
	storageLow bool      // the hive's storage.low is going on: late segments are its notice
	lateRun    int       // checks in a row before this one that found the next segment late
	streams    map[string][]store.SegmentSize
	offline    []recordcheck.Span
	reconnects []time.Time
	clock      *clockReading
	change     *StreamChange
}

// Parts of the day, local time: the night picture is judged against the
// day; dusk and dawn are judged against nothing.
const (
	nightFrom, nightTo = 22, 6
	dayFrom, dayTo     = 8, 20
	patternWindow      = 7 // days of offline minutes
	judgedCover        = 30 * time.Minute
	// restartGrace is how long after the hive or its engine started the
	// first footage may take: the engine connects to the camera and
	// opens a file.
	restartGrace = time.Minute
	ownerWindow  = time.Hour // a stream change this close to the owner's is the owner's
)

type phase int

const (
	twilight phase = iota
	day
	night
)

func phaseOf(t time.Time, loc *time.Location) phase {
	switch h := t.In(loc).Hour(); {
	case h >= nightFrom || h < nightTo:
		return night
	case h >= dayFrom && h < dayTo:
		return day
	}
	return twilight
}

// assess measures every signal and says which reasons hold, given those
// that held before (each clears only past its clear level). A signal that
// cannot be judged now (too little footage, dusk) keeps its reason as it
// was.
func assess(in careInput, prev map[string]bool, th Thresholds) (Signals, map[string]bool) {
	sig := emptySignals()
	on := map[string]bool{}
	for _, name := range streamNames(in.streams) {
		s, drop, dark := bitrate(in, name, prev, th)
		sig.Streams = append(sig.Streams, s)
		on[reasonKey(BitrateDrop, name)] = drop
		on[reasonKey(NightPicture, name)] = dark
	}
	sig.MissingS, sig.LateS, on[string(FrameLoss)] = footage(in, prev[string(FrameLoss)], th)
	sig.ReconnectsHour, sig.Reconnects24h, on[string(Reconnects)] = reconnects(in, prev[string(Reconnects)], th)
	sig.ClockDriftS, sig.ClockCheckedAt, on[string(ClockDrift)] = clockDrift(in.clock, th)
	sig.OfflineMinutes, sig.PatternHours, on[string(OfflinePattern)] = offlinePattern(in, th)
	sig.StreamChange, on[string(StreamChanged)] = streamChange(in, th)
	return sig, on
}

// reasonKey keys a reason that holds per stream ("bitrate_drop/sub").
func reasonKey(r Reason, stream string) string { return string(r) + "/" + stream }

// reasonsOf lists the reasons that hold, in display order, each once.
func reasonsOf(active map[string]bool) []Reason {
	var out []Reason
	for _, r := range AllReasons {
		for key, on := range active {
			if on && (key == string(r) || len(key) > len(r) && key[:len(r)+1] == string(r)+"/") {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func streamNames(streams map[string][]store.SegmentSize) []string {
	names := make([]string, 0, len(streams))
	for n := range streams {
		names = append(names, n)
	}
	sort.Slice(names, func(a, b int) bool { // main first
		return names[a] == string(camera.Main) || names[b] != string(camera.Main) && names[a] < names[b]
	})
	return names
}

// below keeps a reason of a share that must stay above a level: it turns
// on under enter and off again only above leave.
func below(was bool, v, enter, leave float64) bool {
	if was {
		return v <= leave
	}
	return v < enter
}

// rate is bytes over the time they cover.
type rate struct {
	bytes   float64
	covered time.Duration
}

// add counts the part of a closed segment inside [from, to).
func (r *rate) add(row store.SegmentSize, from, to time.Time) {
	if row.Duration <= 0 {
		return
	}
	a, b := later(row.Start, from), earlier(row.Start.Add(row.Duration), to)
	if !b.After(a) {
		return
	}
	part := b.Sub(a)
	r.bytes += float64(row.Bytes) * float64(part) / float64(row.Duration)
	r.covered += part
}

func (r rate) kbps() int64 {
	if r.covered <= 0 {
		return 0
	}
	return int64(r.bytes * 8 / 1000 / r.covered.Seconds())
}

// bitrate measures a stream's last hour against the same clock hour (and
// the hours beside it) of earlier days, so a camera whose scene is quieter
// every evening is normal; and at night against the day: a night hour
// that fell against the camera's own earlier nights and lies far below
// its day is the IR illuminator (night_picture, which then stands for the
// drop too). A night picture clears by day.
func bitrate(in careInput, stream string, prev map[string]bool, th Thresholds) (StreamSignal, bool, bool) {
	rows := in.streams[stream]
	last := window(rows, in.now.Add(-time.Hour), in.now)
	ph := phaseOf(in.now.Add(-30*time.Minute), in.loc)
	s := StreamSignal{Stream: stream, Night: ph == night, UsualKbps: sameHourKbps(rows, in.now, th), DayKbps: dayKbps(rows, in.now, in.loc, th)}
	drop, dark := prev[reasonKey(BitrateDrop, stream)], prev[reasonKey(NightPicture, stream)]
	if ph == day {
		dark = false
	}
	if last.covered < judgedCover {
		return s, drop, dark
	}
	s.LastHourKbps = last.kbps()
	if s.Night && s.UsualKbps > 0 && s.DayKbps > 0 {
		dark = below(dark, float64(s.LastHourKbps)/float64(s.DayKbps), th.NightPicture, th.NightOK) &&
			below(dark, float64(s.LastHourKbps)/float64(s.UsualKbps), th.BitrateDrop, th.BitrateOK)
	}
	if s.UsualKbps > 0 {
		drop = !dark && below(drop, float64(s.LastHourKbps)/float64(s.UsualKbps), th.BitrateDrop, th.BitrateOK)
	}
	return s, drop, dark
}

// window is the bitrate of the segments over [from, to).
func window(rows []store.SegmentSize, from, to time.Time) rate {
	var r rate
	for _, row := range rows {
		r.add(row, from, to)
	}
	return r
}

// sameHourKbps is the usual bitrate of the last hour: the median of the
// same hour, and the hours an hour before and after it, on each of the
// last BaselineDays days, counting windows with half an hour of footage;
// 0 while fewer than BaselineMinDays days have one.
func sameHourKbps(rows []store.SegmentSize, now time.Time, th Thresholds) int64 {
	var ks []int64
	days := 0
	for d := 1; d <= th.BaselineDays; d++ {
		end := now.AddDate(0, 0, -d)
		had := false
		for _, o := range []time.Duration{-time.Hour, 0, time.Hour} {
			if w := window(rows, end.Add(o-time.Hour), end.Add(o)); w.covered >= judgedCover {
				ks, had = append(ks, w.kbps()), true
			}
		}
		if had {
			days++
		}
	}
	if days < th.BaselineMinDays {
		return 0
	}
	return median(ks)
}

// dayKbps is the median bitrate of the day hours (local) before the last
// hour, of the last BaselineDays days; 0 with fewer than three such hours.
func dayKbps(rows []store.SegmentSize, now time.Time, loc *time.Location, th Thresholds) int64 {
	until := now.Add(-time.Hour)
	var ks []int64
	for h := hourStart(now.AddDate(0, 0, -th.BaselineDays), loc); !h.Add(time.Hour).After(until); h = h.Add(time.Hour) {
		if phaseOf(h, loc) != day {
			continue
		}
		if w := window(rows, h, h.Add(time.Hour)); w.covered >= judgedCover {
			ks = append(ks, w.kbps())
		}
	}
	if len(ks) < 3 {
		return 0
	}
	return median(ks)
}

func median(ks []int64) int64 {
	slices.Sort(ks)
	n := len(ks)
	if n%2 == 1 {
		return ks[n/2]
	}
	return (ks[n/2-1] + ks[n/2]) / 2
}

func hourStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, loc)
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// footage is the main stream's missing footage in the last hour and how
// late its next segment is while it is online. Time the Camera was
// offline (its own notification) and time before the hive or its engine
// started (a restart, a reboot, an update: nothing could record) is not
// missing. A late segment counts once it was late on LateChecks checks in
// a row, and not while the hive's storage is low (that has its notice).
func footage(in careInput, was bool, th Thresholds) (missingS, lateS float64, on bool) {
	from := later(in.now.Add(-time.Hour), in.restarted.Add(restartGrace))
	rows := in.streams[string(camera.Main)]
	files := make([]recordcheck.File, 0, len(rows))
	var newest time.Time
	for _, r := range rows {
		files = append(files, recordcheck.File{Start: r.Start, Duration: r.Duration})
		newest = later(newest, r.Start)
	}
	var missing time.Duration
	if from.Before(in.now) {
		for _, g := range recordcheck.Gaps(files, from, in.now) {
			missing += g.To.Sub(g.From) - overlap(in.offline, g.From, g.To)
		}
	}
	var late time.Duration
	if in.online && !newest.IsZero() {
		late = recordcheck.Late(later(newest, in.restarted), in.now, in.segment)
	}
	missingS, lateS = missing.Seconds(), late.Seconds()
	limit := th.MissingS
	if was {
		limit = th.MissingOKS
	}
	lateOn := late > 0 && !in.storageLow && in.lateRun+1 >= th.LateChecks
	return missingS, lateS, missingS > limit || lateOn
}

func reconnects(in careInput, was bool, th Thresholds) (hour, day int, on bool) {
	from := in.now.Add(-time.Hour)
	for _, t := range in.reconnects {
		if !t.Before(from) {
			hour++
		}
	}
	day = len(in.reconnects)
	if was {
		return hour, day, hour > th.ReconnectsOK
	}
	return hour, day, hour >= th.Reconnects
}

func clockDrift(c *clockReading, th Thresholds) (*float64, *time.Time, bool) {
	if c == nil {
		return nil, nil, false
	}
	s, at := c.drift.Seconds(), c.at
	return &s, &at, c.drift.Abs().Seconds() > th.ClockDriftS
}

// offlinePattern is the Camera's offline minutes per local day of the last
// week, and the hours of day it was offline for at least PatternMinutes
// on PatternDays of them; days it was offline half a day or more are an
// outage, not a pattern, and do not count.
func offlinePattern(in careInput, th Thresholds) ([]DayMinutes, []int, bool) {
	today := dayStart(in.now, in.loc)
	days := make([]DayMinutes, 0, patternWindow)
	var count [24]int
	for k := patternWindow - 1; k >= 0; k-- {
		d := today.AddDate(0, 0, -k)
		off := overlap(in.offline, d, earlier(d.AddDate(0, 0, 1), in.now))
		days = append(days, DayMinutes{Day: d.Format(time.DateOnly), Minutes: int(off / time.Minute)})
		if off >= 12*time.Hour {
			continue
		}
		for h := range 24 {
			at := time.Date(d.Year(), d.Month(), d.Day(), h, 0, 0, 0, in.loc)
			if overlap(in.offline, at, at.Add(time.Hour)) >= time.Duration(th.PatternMinutes)*time.Minute {
				count[h]++
			}
		}
	}
	hours := []int{}
	for h, n := range count {
		if n >= th.PatternDays {
			hours = append(hours, h)
		}
	}
	return days, hours, len(hours) > 0
}

// streamChange reports the latest codec or resolution change for
// StreamChangedH hours, unless the owner changed the Camera around it.
func streamChange(in careInput, th Thresholds) (*StreamChange, bool) {
	ch := in.change
	if ch == nil || in.now.Sub(ch.At) >= time.Duration(th.StreamChangedH)*time.Hour || !in.updated.Before(ch.At.Add(-ownerWindow)) {
		return nil, false
	}
	return ch, true
}

// videoChanged says whether a stream's video differs from before: its
// codec, or its size where both are known.
func videoChanged(before, now video.Track) bool {
	switch {
	case before.Codec == "":
		return false
	case before.Codec != now.Codec:
		return true
	}
	known := before.Width > 0 && now.Width > 0
	return known && (before.Width != now.Width || before.Height != now.Height)
}

// storageLow reports whether the hive's storage.low is going on at now.
func storageLow(evs []store.Event, now time.Time) bool {
	for _, e := range evs {
		if e.Kind == "storage.low" && e.Camera == "" && !e.At.After(now) && (e.End.IsZero() || e.End.After(now)) {
			return true
		}
	}
	return false
}

// offlineSpans turns the timeline's camera.offline and camera.online
// Events into the spans the Camera was offline; one still going on ends
// now.
func offlineSpans(evs []store.Event, now time.Time) []recordcheck.Span {
	var out []recordcheck.Span
	var start time.Time
	for _, e := range evs {
		switch {
		case e.Kind == "camera.offline" && start.IsZero():
			start = e.At
		case e.Kind == "camera.online" && !start.IsZero():
			out = append(out, recordcheck.Span{From: start, To: e.At})
			start = time.Time{}
		}
	}
	if !start.IsZero() {
		out = append(out, recordcheck.Span{From: start, To: now})
	}
	return out
}

// overlap is how much of [from, to) the spans cover.
func overlap(spans []recordcheck.Span, from, to time.Time) time.Duration {
	var sum time.Duration
	for _, s := range spans {
		if a, b := later(s.From, from), earlier(s.To, to); b.After(a) {
			sum += b.Sub(a)
		}
	}
	return sum
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
