package motion

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/events"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// Options configure a Hub.
type Options struct {
	Config  Config
	Tick    time.Duration     // how often trackers are ticked; default 1s
	Forget  time.Duration     // a camera quiet this long with nothing open is forgotten; default 10min
	Observe func(store.Event) // told when an episode begins and ends (notifications, #66)
	Calm    CalmRule          // auto-calm (#157)
	OnCalm  func(Calming)     // told when a camera is calmed and when it steps back (the action log)
	Logger  *slog.Logger
}

// input is one message to the Hub's loop; exactly one of the pointers is set.
type input struct {
	cam    string
	frame  *video.Frame
	live   *bool
	motion *cameraMotion
	sens   *camera.Sensitivity
	known  []string // with sens == nil and the rest nil: the cameras the store has
}

type cameraMotion struct {
	on bool
	at time.Time
}

// Hub runs one Tracker per Camera on a single goroutine and records their
// episodes as motion Events. Frames from the engine are dropped, never
// waited for, when the loop falls behind.
type Hub struct {
	sink    events.Sink
	opts    Options
	log     *slog.Logger
	in      chan input
	done    chan struct{} // closed when Run returns
	dropped atomic.Int64
	tracked atomic.Int64 // cameras the loop holds

	calmMu sync.Mutex
	calmed map[string]time.Time // written by the loop, read by the API
}

// NewHub creates a Hub; Run drives it.
func NewHub(sink events.Sink, opts Options) *Hub {
	if opts.Config == (Config{}) {
		opts.Config = Defaults()
	}
	if opts.Tick == 0 {
		opts.Tick = time.Second
	}
	if opts.Forget == 0 {
		opts.Forget = 10 * time.Minute
	}
	if opts.Observe == nil {
		opts.Observe = func(store.Event) {}
	}
	if opts.OnCalm == nil {
		opts.OnCalm = func(Calming) {}
	}
	opts.Calm = opts.Calm.withDefaults()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Hub{sink: sink, opts: opts, log: opts.Logger, in: make(chan input, 1024), done: make(chan struct{}), calmed: map[string]time.Time{}}
}

// Frame takes one frame of a Camera's analysed Stream. It never blocks.
func (h *Hub) Frame(cam video.CameraID, _ video.Stream, f video.Frame) {
	select {
	case h.in <- input{cam: string(cam), frame: &f}:
	default:
		h.dropped.Add(1)
	}
}

// CameraSource says whether a Camera's own motion events are available.
func (h *Hub) CameraSource(cam string, live bool) { h.send(input{cam: cam, live: &live}) }

// CameraMotion is a Camera reporting motion on or off.
func (h *Hub) CameraMotion(cam string, on bool, at time.Time) {
	h.send(input{cam: cam, motion: &cameraMotion{on: on, at: at}})
}

// Sensitivity sets a Camera's motion sensitivity (the Watcher passes the
// store's on every reconcile).
func (h *Hub) Sensitivity(cam string, s camera.Sensitivity) { h.send(input{cam: cam, sens: &s}) }

// Known tells the Hub which Cameras exist (the Watcher passes the store's
// after their sensitivities); it forgets the sensitivity and calm of any
// other.
func (h *Hub) Known(cams []string) {
	if cams == nil {
		cams = []string{}
	}
	h.send(input{known: cams})
}

// CalmedSince says since when the Hub has calmed a Camera (#157).
func (h *Hub) CalmedSince(cam string) (time.Time, bool) {
	h.calmMu.Lock()
	defer h.calmMu.Unlock()
	t, ok := h.calmed[cam]
	return t, ok
}

func (h *Hub) setCalmed(cam string, since time.Time) {
	h.calmMu.Lock()
	defer h.calmMu.Unlock()
	if since.IsZero() {
		delete(h.calmed, cam)
	} else {
		h.calmed[cam] = since
	}
}

// send waits for room: unlike a frame, a state report must not be lost.
// After Run has returned it is dropped.
func (h *Hub) send(in input) {
	select {
	case h.in <- in:
	case <-h.done:
	}
}

// tracked is a Tracker, the Condition that records its episodes and when
// the camera last sent anything.
type tracked struct {
	tr   *Tracker
	cond *events.Condition
	seen time.Time
}

// loop is the state Run owns: the trackers, each camera's sensitivity and
// its auto-calm. Sensitivity and calm outlive a forgotten tracker.
type loop struct {
	h    *Hub
	cams map[string]*tracked
	sens map[string]camera.Sensitivity
	calm map[string]*calmState
}

// Run processes input and ticks until ctx ends, then ends open episodes.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	l := &loop{h: h, cams: map[string]*tracked{}, sens: map[string]camera.Sensitivity{}, calm: map[string]*calmState{}}
	tick := time.NewTicker(h.opts.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			l.stop(ctx)
			return
		case in := <-h.in:
			l.take(ctx, in)
		case now := <-tick.C:
			l.tick(ctx, now)
		}
		h.tracked.Store(int64(len(l.cams)))
	}
}

func (l *loop) get(id string) *tracked {
	c, ok := l.cams[id]
	if !ok {
		h := l.h
		c = &tracked{tr: NewTracker(h.opts.Config), cond: events.NewCondition(h.sink, id, Kind, events.Observe(h.opts.Observe), events.WithLogger(h.log))}
		c.tr.SetLevel(l.level(id))
		l.cams[id] = c
	}
	return c
}

// level is a camera's Level: its sensitivity, one step less while calmed.
func (l *loop) level(id string) Level {
	s, ok := l.calm[id]
	return LevelFor(l.sens[id], ok && !s.since.IsZero())
}

// relevel applies a changed sensitivity or calm to a tracked camera.
func (l *loop) relevel(id string) {
	if c, ok := l.cams[id]; ok {
		c.tr.SetLevel(l.level(id))
	}
}

func (l *loop) take(ctx context.Context, in input) {
	if in.sens != nil { // not a sign of life: it comes from the store, not the camera
		l.sens[in.cam] = *in.sens
		l.relevel(in.cam)
		return
	}
	if in.known != nil {
		l.forgetOthers(in.known)
		return
	}
	c := l.get(in.cam)
	c.seen = time.Now()
	l.apply(ctx, in.cam, c, step(c.tr, in))
}

// forgetOthers drops the sensitivity and calm of cameras the store no
// longer has (removed); a tracker still running goes back to normal.
func (l *loop) forgetOthers(known []string) {
	keep := map[string]bool{}
	for _, id := range known {
		keep[id] = true
	}
	for _, m := range []map[string]bool{keysOf(l.sens), keysOf(l.calm)} {
		for id := range m {
			if !keep[id] {
				delete(l.sens, id)
				delete(l.calm, id)
				l.h.setCalmed(id, time.Time{})
				l.relevel(id)
			}
		}
	}
}

func keysOf[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func (l *loop) tick(ctx context.Context, now time.Time) {
	for id, c := range l.cams {
		l.apply(ctx, id, c, c.tr.Tick(now))
		if !c.tr.Open() && now.Sub(c.seen) >= l.h.opts.Forget { // removed or long offline
			delete(l.cams, id)
		}
	}
	for id, s := range l.calm {
		if s.quiet(now, l.h.opts.Calm) {
			l.h.setCalmed(id, time.Time{})
			l.relevel(id)
			l.h.opts.OnCalm(Calming{Camera: id, At: now, Sensitivity: l.sens[id]})
		}
		if s.since.IsZero() && now.Sub(s.begins[len(s.begins)-1]) >= l.h.opts.Calm.Window {
			delete(l.calm, id) // nothing left to count
		}
	}
}

// stop ends every open episode at its last activity, with a little time to
// write that survives the cancelled ctx.
func (l *loop) stop(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for id, c := range l.cams {
		l.apply(ctx, id, c, c.tr.Stop())
	}
}

func step(tr *Tracker, in input) []Change {
	switch {
	case in.frame != nil:
		return tr.Frame(*in.frame)
	case in.live != nil:
		tr.CameraSource(*in.live)
		return nil
	default:
		return tr.CameraMotion(in.motion.on, in.motion.at)
	}
}

func (l *loop) apply(ctx context.Context, id string, c *tracked, changes []Change) {
	for _, ch := range changes {
		if !ch.Begin {
			c.cond.End(ctx, ch.At)
			continue
		}
		c.cond.Begin(ctx, ch.At, map[string]any{"source": ch.Source})
		l.counted(id, ch.At)
	}
}

// counted feeds an episode start to the camera's auto-calm.
func (l *loop) counted(id string, at time.Time) {
	s, ok := l.calm[id]
	if !ok {
		s = &calmState{}
		l.calm[id] = s
	}
	calms, n := s.begin(at, l.h.opts.Calm)
	if !calms {
		return
	}
	l.h.setCalmed(id, at)
	l.relevel(id)
	l.h.opts.OnCalm(Calming{Camera: id, Calmed: true, At: at, Episodes: n, Sensitivity: l.sens[id]})
}

// Cameras reports how many cameras the hub is tracking.
func (h *Hub) Cameras() int { return int(h.tracked.Load()) }

// Dropped reports how many frames were dropped because the loop was busy.
func (h *Hub) Dropped() int64 { return h.dropped.Load() }
