package motion

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

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
	Logger  *slog.Logger
}

// input is one message to the Hub's loop; exactly one of the pointers is set.
type input struct {
	cam    string
	frame  *video.Frame
	live   *bool
	motion *cameraMotion
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
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Hub{sink: sink, opts: opts, log: opts.Logger, in: make(chan input, 1024), done: make(chan struct{})}
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

// Run processes input and ticks until ctx ends, then ends open episodes.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	cams := map[string]*tracked{}
	get := func(id string) *tracked {
		c, ok := cams[id]
		if !ok {
			c = &tracked{tr: NewTracker(h.opts.Config), cond: events.NewCondition(h.sink, id, Kind, events.Observe(h.opts.Observe), events.WithLogger(h.log))}
			cams[id] = c
		}
		return c
	}
	tick := time.NewTicker(h.opts.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			h.stop(ctx, cams)
			return
		case in := <-h.in:
			c := get(in.cam)
			c.seen = time.Now()
			h.apply(ctx, c, h.step(c.tr, in))
		case now := <-tick.C:
			for id, c := range cams {
				h.apply(ctx, c, c.tr.Tick(now))
				if !c.tr.Open() && now.Sub(c.seen) >= h.opts.Forget { // removed or long offline
					delete(cams, id)
				}
			}
		}
		h.tracked.Store(int64(len(cams)))
	}
}

// stop ends every open episode at its last activity, with a little time to
// write that survives the cancelled ctx.
func (h *Hub) stop(ctx context.Context, cams map[string]*tracked) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, c := range cams {
		h.apply(ctx, c, c.tr.Stop())
	}
}

func (h *Hub) step(tr *Tracker, in input) []Change {
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

func (h *Hub) apply(ctx context.Context, c *tracked, changes []Change) {
	for _, ch := range changes {
		if ch.Begin {
			c.cond.Begin(ctx, ch.At, map[string]any{"source": ch.Source})
		} else {
			c.cond.End(ctx, ch.At)
		}
	}
}

// Cameras reports how many cameras the hub is tracking.
func (h *Hub) Cameras() int { return int(h.tracked.Load()) }

// Dropped reports how many frames were dropped because the loop was busy.
func (h *Hub) Dropped() int64 { return h.dropped.Load() }
