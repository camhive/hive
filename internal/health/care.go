package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// Camera care (#163): the hive watches what it already has about every
// Camera (the index's bytes per segment, the monitor's polls, the
// timeline's offline episodes, the camera's own clock) and says when a
// Camera is getting worse before footage goes missing. A Camera is
// degraded while at least one Reason holds; one degraded episode is one
// camera.degraded Event, ended by camera.recovered.

// Event kinds of camera care.
const (
	EventDegraded  = "camera.degraded"
	EventRecovered = "camera.recovered"
)

// Reason says why a Camera is degraded.
type Reason string

// Reasons, in the order the app lists them.
const (
	BitrateDrop    Reason = "bitrate_drop"    // a stream sent much less than usual at this part of the day
	FrameLoss      Reason = "frame_loss"      // footage missing between segments, or segments arriving late
	Reconnects     Reason = "reconnects"      // the stream dropped and came back again and again
	ClockDrift     Reason = "clock_drift"     // the camera's clock is off the hive's
	OfflinePattern Reason = "offline_pattern" // offline at the same hour on several days
	NightPicture   Reason = "night_picture"   // the night picture collapsed against the day's
	StreamChanged  Reason = "stream_changed"  // codec or resolution changed without the owner
)

// AllReasons lists every Reason in display order.
var AllReasons = []Reason{BitrateDrop, FrameLoss, Reconnects, ClockDrift, OfflinePattern, NightPicture, StreamChanged}

// Suggestion is what the owner can do about a Reason, in plain language.
func (r Reason) Suggestion() string {
	switch r {
	case BitrateDrop:
		return "A quieter scene also lowers the bitrate; if the picture looks wrong, check the cable or the PoE budget."
	case Reconnects:
		return "Check the cable or the PoE budget."
	case FrameLoss:
		return "Check the cable, then update the camera's firmware."
	case ClockDrift:
		return "Set the camera's NTP server."
	case OfflinePattern:
		return "Check what switches the camera's power or network off at those hours."
	case NightPicture:
		return "The IR illuminator may have failed."
	case StreamChanged:
		return "Check the camera's video settings, and update the camera's firmware if it reset them."
	}
	return ""
}

// Thresholds are where a signal becomes a Reason and where it clears
// again; the gap between the two keeps a Reason from flapping. Bitrate,
// missing footage and reconnects are measured over the last hour; late
// segments must repeat on LateChecks checks; the clock and a stream change
// are single readings; the offline pattern looks at a week.
type Thresholds struct {
	BitrateDrop     float64 `json:"bitrate_drop"`      // the last hour below this share of the usual bitrate
	BitrateOK       float64 `json:"bitrate_ok"`        // clears above this share
	NightPicture    float64 `json:"night_picture"`     // a night hour below this share of the day's bitrate
	NightOK         float64 `json:"night_ok"`          // clears above this share
	MissingS        float64 `json:"missing_s"`         // seconds of footage missing in the last hour
	MissingOKS      float64 `json:"missing_ok_s"`      // clears at or below this
	Reconnects      int     `json:"reconnects"`        // reconnects in the last hour
	ReconnectsOK    int     `json:"reconnects_ok"`     // clears at or below this
	ClockDriftS     float64 `json:"clock_drift_s"`     // seconds the camera's clock may be off
	ClockEveryH     int     `json:"clock_every_h"`     // hours between two readings of the camera's clock
	PatternDays     int     `json:"pattern_days"`      // days of the last 7 offline in the same hour
	PatternMinutes  int     `json:"pattern_minutes"`   // offline minutes that make an hour count
	StreamChangedH  int     `json:"stream_changed_h"`  // hours a codec or resolution change is reported
	BaselineDays    int     `json:"baseline_days"`     // days that make the usual bitrate of an hour
	BaselineMinDays int     `json:"baseline_min_days"` // with fewer days of history there is no judgement
	LateChecks      int     `json:"late_checks"`       // checks in a row a segment must be late
}

// DefaultThresholds are the hive's thresholds (docs/user/cameras.md).
func DefaultThresholds() Thresholds {
	return Thresholds{BitrateDrop: 0.6, BitrateOK: 0.8, NightPicture: 0.25, NightOK: 0.35, MissingS: 30, MissingOKS: 5,
		Reconnects: 4, ReconnectsOK: 1, ClockDriftS: 30, ClockEveryH: 6, PatternDays: 3, PatternMinutes: 5,
		StreamChangedH: 24, BaselineDays: 7, BaselineMinDays: 3, LateChecks: 3}
}

// Sample is one poll of one stream as the Monitor saw it.
type Sample struct {
	Camera, Stream string
	At             time.Time
	Ready          bool
	Status         video.Status
	Err            error // the engine did not answer: says nothing about the camera
}

// SegmentSizer is the part of the index care reads; *store.Segments is one.
type SegmentSizer interface {
	Sizes(ctx context.Context, camera, stream string, from, to time.Time) ([]store.SegmentSize, error)
}

// EventRanger is the part of the timeline care reads; *store.Events is one.
type EventRanger interface {
	Range(ctx context.Context, camera string, from, to time.Time) ([]store.Event, error)
}

// CareSources are what care reads.
type CareSources struct {
	Cameras  CameraLister
	Segments SegmentSizer
	Events   EventRanger
	// Clock reads an ONVIF camera's clock; nil reads none.
	Clock func(ctx context.Context, c camera.Camera) (time.Time, error)
}

// CareOptions tune care.
type CareOptions struct {
	Now        func() time.Time
	Started    time.Time      // when the hive started; zero: now. Footage before it is not missing
	Location   *time.Location // day and night, and the days of offline minutes; default time.Local
	Segment    time.Duration  // the recording segment length; default 10 minutes
	Thresholds Thresholds     // zero: DefaultThresholds
	// OnDegraded is called on every check while a Camera is degraded,
	// with the episode's start and the reasons that hold now (the first
	// call begins the episode; later ones let a failed write retry).
	OnDegraded func(camera string, since time.Time, reasons []Reason)
	// OnRecovered ends the episode at `at`; recovered is false when the
	// Camera was disabled or removed rather than well again.
	OnRecovered func(camera string, at time.Time, recovered bool)
	Logger      *slog.Logger
}

// Care watches every enabled Camera. Observe feeds it the Monitor's polls,
// Check (every few minutes, Run) judges. Safe for concurrent use.
type Care struct {
	src  CareSources
	opts CareOptions

	mu        sync.Mutex
	cams      map[string]*careState
	restarted time.Time // the hive or its engine last started
}

type careState struct {
	streams    map[string]*pollState // from Observe
	reconnects []time.Time           // of the main stream, the last 24 hours
	change     *StreamChange         // the latest codec or resolution change
	clock      *clockReading
	clockDue   time.Time       // when to read the camera's clock next
	lateRun    int             // checks in a row that found the next segment late
	active     map[string]bool // reasons that hold, by reasonKey
	since      time.Time       // the episode's start; zero while not degraded
	view       CameraCare
}

type pollState struct {
	seen, ready bool
	bytes       int64
	video       video.Track
}

type clockReading struct {
	at    time.Time
	drift time.Duration
}

// NewCare creates a Care.
func NewCare(src CareSources, opts CareOptions) *Care {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Segment == 0 {
		opts.Segment = 10 * time.Minute
	}
	if opts.Thresholds == (Thresholds{}) {
		opts.Thresholds = DefaultThresholds()
	}
	if opts.OnDegraded == nil {
		opts.OnDegraded = func(string, time.Time, []Reason) {}
	}
	if opts.OnRecovered == nil {
		opts.OnRecovered = func(string, time.Time, bool) {}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Started.IsZero() {
		opts.Started = opts.Now()
	}
	return &Care{src: src, opts: opts, cams: map[string]*careState{}, restarted: opts.Started}
}

// EngineStarted tells care the video engine (re)started at `at`: footage
// before it is not missing, and the streams coming up are no reconnects.
func (c *Care) EngineStarted(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restarted = later(c.restarted, at)
}

func (c *Care) state(id string) *careState {
	cs := c.cams[id]
	if cs == nil {
		cs = &careState{streams: map[string]*pollState{}, active: map[string]bool{}}
		c.cams[id] = cs
	}
	return cs
}

// Observe takes one poll: a main stream that comes back after a poll
// that found it not ready, or whose byte counter starts again,
// reconnected (not while the hive or its engine is starting); a poll the
// engine did not answer says nothing. A video track that changes codec
// or size changed the stream.
func (c *Care) Observe(s Sample) {
	if s.Err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cs := c.state(s.Camera)
	ps := cs.streams[s.Stream]
	if ps == nil {
		ps = &pollState{}
		cs.streams[s.Stream] = ps
	}
	if !s.Ready {
		ps.ready = false
		return
	}
	starting := s.At.Before(c.restarted.Add(restartGrace))
	if s.Stream == string(camera.Main) && ps.seen && !starting && (!ps.ready || s.Status.BytesReceived < ps.bytes) {
		cs.reconnects = append(since(cs.reconnects, s.At.Add(-24*time.Hour)), s.At)
	}
	if v := s.Status.Video; v.Codec != "" {
		if videoChanged(ps.video, v) {
			cs.change = &StreamChange{Stream: s.Stream, At: s.At, From: ps.video, To: v}
		}
		ps.video = v
	}
	ps.seen, ps.ready, ps.bytes = true, true, s.Status.BytesReceived
}

// Run checks every interval until ctx is cancelled.
func (c *Care) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := c.Check(ctx); err != nil {
			c.opts.Logger.Warn("camera care", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check judges every enabled Camera once, begins and ends episodes, and
// forgets Cameras that are disabled or gone (ending their episodes).
func (c *Care) Check(ctx context.Context) error {
	cams, err := c.src.Cameras.List(ctx)
	if err != nil {
		return err
	}
	now := c.opts.Now()
	watched := map[string]bool{}
	var errs []error
	for _, cam := range cams {
		if !cam.Enabled {
			continue
		}
		watched[string(cam.ID)] = true
		if err := c.checkCamera(ctx, cam, now); err != nil {
			errs = append(errs, err)
		}
	}
	c.forget(watched, now)
	return errors.Join(errs...)
}

func (c *Care) checkCamera(ctx context.Context, cam camera.Camera, now time.Time) error {
	in, err := c.gather(ctx, cam, now)
	if err != nil {
		return err
	}
	id := string(cam.ID)
	c.mu.Lock()
	cs := c.state(id)
	if in.clock != nil {
		cs.clock = in.clock
	}
	cs.reconnects = since(cs.reconnects, now.Add(-24*time.Hour))
	in.reconnects, in.change, in.clock = notOwners(cs.reconnects, cam.UpdatedAt), cs.change, cs.clock
	in.online = cs.streams[string(camera.Main)] != nil && cs.streams[string(camera.Main)].ready
	in.restarted, in.lateRun = c.restarted, cs.lateRun
	sig, active := assess(in, cs.active, c.opts.Thresholds)
	if sig.LateS > 0 {
		cs.lateRun++
	} else {
		cs.lateRun = 0
	}
	if sig.StreamChange == nil {
		cs.change = nil // reported long enough, or the owner's own
	}
	cs.active = active
	reasons := reasonsOf(active)
	ended := c.episode(cs, reasons, now)
	episodeStart := cs.since
	cs.view = c.view(id, cs, sig, reasons, now)
	c.mu.Unlock()
	if !episodeStart.IsZero() {
		c.opts.OnDegraded(id, episodeStart, reasons)
	}
	if ended {
		c.opts.OnRecovered(id, now, true)
	}
	return nil
}

// ownerEdit is how long after the owner changed a Camera its streams may
// restart (the engine re-adds the source) without it being a reconnect.
const ownerEdit = 2 * time.Minute

// notOwners drops the reconnects right after the owner's own change.
func notOwners(ts []time.Time, updated time.Time) []time.Time {
	out := make([]time.Time, 0, len(ts))
	for _, t := range ts {
		if t.Before(updated) || !t.Before(updated.Add(ownerEdit)) {
			out = append(out, t)
		}
	}
	return out
}

// episode begins or ends the degraded episode; it reports an end.
func (c *Care) episode(cs *careState, reasons []Reason, now time.Time) (ended bool) {
	switch {
	case len(reasons) > 0 && cs.since.IsZero():
		cs.since = now
	case len(reasons) == 0 && !cs.since.IsZero():
		cs.since = time.Time{}
		return true
	}
	return false
}

func (c *Care) forget(watched map[string]bool, now time.Time) {
	var ended []string
	c.mu.Lock()
	for id, cs := range c.cams {
		if watched[id] {
			continue
		}
		if !cs.since.IsZero() {
			ended = append(ended, id)
		}
		delete(c.cams, id)
	}
	c.mu.Unlock()
	for _, id := range ended {
		c.opts.OnRecovered(id, now, false) // disabled or removed: not well again
	}
}

// gather reads the index, the timeline and, when due, the camera's clock.
func (c *Care) gather(ctx context.Context, cam camera.Camera, now time.Time) (careInput, error) {
	th := c.opts.Thresholds
	id := string(cam.ID)
	in := careInput{now: now, loc: c.opts.Location, segment: c.opts.Segment, updated: cam.UpdatedAt, streams: map[string][]store.SegmentSize{}}
	from := now.Add(-time.Duration(th.BaselineDays)*24*time.Hour - time.Hour)
	for name := range cam.Streams {
		rows, err := c.src.Segments.Sizes(ctx, id, string(name), from, now)
		if err != nil {
			return in, err
		}
		in.streams[string(name)] = rows
	}
	evs, err := c.src.Events.Range(ctx, id, dayStart(now, in.loc).AddDate(0, 0, -patternWindow+1), now)
	if err != nil {
		return in, err
	}
	in.offline = offlineSpans(evs, now)
	in.storageLow = storageLow(evs, now)
	in.clock = c.readClock(ctx, cam, now)
	return in, nil
}

// clockTimeout bounds one reading of a camera's clock; clockRetry is when
// a camera that did not answer is asked again.
const (
	clockTimeout = 5 * time.Second
	clockRetry   = 30 * time.Minute
)

// readClock reads an ONVIF camera's clock when due (every ClockEveryH, or
// clockRetry after a failed reading); nil when it did not read or the
// camera did not answer, which keeps the last reading.
func (c *Care) readClock(ctx context.Context, cam camera.Camera, now time.Time) *clockReading {
	if c.src.Clock == nil || cam.Source.Kind != camera.SourceONVIF {
		return nil
	}
	c.mu.Lock()
	cs := c.state(string(cam.ID))
	due := !now.Before(cs.clockDue)
	if due {
		cs.clockDue = now.Add(clockRetry)
	}
	c.mu.Unlock()
	if !due {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, clockTimeout)
	defer cancel()
	theirs, err := c.src.Clock(ctx, cam)
	if err != nil {
		c.opts.Logger.Info("camera clock", "camera", cam.ID, "err", err)
		return nil
	}
	ours := c.opts.Now()
	c.mu.Lock()
	cs.clockDue = now.Add(time.Duration(c.opts.Thresholds.ClockEveryH) * time.Hour)
	c.mu.Unlock()
	return &clockReading{at: ours, drift: theirs.Sub(ours).Round(time.Second)}
}

// CameraCare is what care says about one Camera (GET /v0/cameras/{id}/care).
type CameraCare struct {
	Camera     string       `json:"camera"`
	CheckedAt  *time.Time   `json:"checked_at"` // nil before the first check
	Degraded   bool         `json:"degraded"`
	Since      *time.Time   `json:"since,omitempty"` // the episode's start
	Reasons    []ReasonView `json:"reasons"`
	Signals    Signals      `json:"signals"`
	Thresholds Thresholds   `json:"thresholds"`
}

// CareSummary is what the camera list's badge needs.
type CareSummary struct {
	Degraded bool     `json:"degraded"`
	Reasons  []Reason `json:"reasons"`
}

// Summary is the view's degraded flag and reasons.
func (v CameraCare) Summary() *CareSummary {
	s := &CareSummary{Degraded: v.Degraded, Reasons: make([]Reason, 0, len(v.Reasons))}
	for _, r := range v.Reasons {
		s.Reasons = append(s.Reasons, r.Reason)
	}
	return s
}

// ReasonView is a Reason with its suggestion.
type ReasonView struct {
	Reason     Reason `json:"reason"`
	Suggestion string `json:"suggestion"`
}

func (c *Care) view(id string, cs *careState, sig Signals, reasons []Reason, now time.Time) CameraCare {
	v := CameraCare{Camera: id, CheckedAt: &now, Degraded: len(reasons) > 0, Reasons: make([]ReasonView, 0, len(reasons)),
		Signals: sig, Thresholds: c.opts.Thresholds}
	for _, r := range reasons {
		v.Reasons = append(v.Reasons, ReasonView{Reason: r, Suggestion: r.Suggestion()})
	}
	if !cs.since.IsZero() {
		at := cs.since
		v.Since = &at
	}
	return v
}

// Camera returns what care says about a Camera; before its first check
// there are no signals.
func (c *Care) Camera(id string) CameraCare {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cs, ok := c.cams[id]; ok && cs.view.Camera != "" {
		return cs.view
	}
	return CameraCare{Camera: id, Reasons: []ReasonView{}, Signals: emptySignals(), Thresholds: c.opts.Thresholds}
}

// since drops the times before from.
func since(ts []time.Time, from time.Time) []time.Time {
	k := 0
	for k < len(ts) && ts[k].Before(from) {
		k++
	}
	return ts[k:]
}
