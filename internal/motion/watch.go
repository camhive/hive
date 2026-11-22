package motion

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/onvif"
)

// CameraLister is the part of the store the Watcher needs.
type CameraLister interface {
	List(ctx context.Context) ([]camera.Camera, error)
}

// Reporter is told what a camera's own detector says and each camera's
// sensitivity; *Hub is one.
type Reporter interface {
	CameraSource(cam string, live bool)
	CameraMotion(cam string, on bool, at time.Time)
	Sensitivity(cam string, s camera.Sensitivity)
	Known(cams []string)
}

// WatchOptions tune the Watcher.
type WatchOptions struct {
	Interval   time.Duration // reconcile with the store; default 30s
	MinBackoff time.Duration // after a broken subscription; default 5s
	MaxBackoff time.Duration // default 5min
	NoEvents   time.Duration // wait before asking a camera without events again; default 1h
	Logger     *slog.Logger
}

// Watcher keeps a PullPoint subscription to every enabled ONVIF Camera and
// tells the Hub what the camera's own detector reports (ADR-0020), and
// every Camera's sensitivity (#157). The
// camera source counts as live from its first motion message on: a camera
// whose detection is off never sends one and stays on the frames source.
type Watcher struct {
	cams CameraLister
	hub  Reporter
	opts WatchOptions
	kick chan struct{}

	mu      sync.Mutex
	running map[camera.ID]*watch
}

// watch is one camera's subscription loop; it restarts when the address or
// credentials change.
type watch struct {
	key    watchKey
	cancel context.CancelFunc
	done   chan struct{}
}

type watchKey struct {
	address string
	creds   camera.Credentials
}

// NewWatcher creates a Watcher; Run drives it.
func NewWatcher(cams CameraLister, hub Reporter, opts WatchOptions) *Watcher {
	if opts.Interval == 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.MinBackoff == 0 {
		opts.MinBackoff = 5 * time.Second
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 5 * time.Minute
	}
	if opts.NoEvents == 0 {
		opts.NoEvents = time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Watcher{cams: cams, hub: hub, opts: opts, kick: make(chan struct{}, 1), running: map[camera.ID]*watch{}}
}

// Notify asks the running loop to reconcile now (after a camera change).
func (w *Watcher) Notify() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Watching returns how many cameras are being watched.
func (w *Watcher) Watching() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.running)
}

// Run reconciles until ctx ends, then stops every watch.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.opts.Interval)
	defer t.Stop()
	defer w.stopAll()
	for {
		if err := w.reconcile(ctx); err != nil {
			w.opts.Logger.Warn("motion: list cameras", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
		}
	}
}

// reconcile starts a watch for every enabled ONVIF camera and stops the
// others; a changed address or credentials restart it.
func (w *Watcher) reconcile(ctx context.Context) error {
	cams, err := w.cams.List(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(cams))
	for _, c := range cams {
		w.hub.Sensitivity(string(c.ID), c.Sensitivity)
		ids = append(ids, string(c.ID))
	}
	w.hub.Known(ids)
	want := watched(cams)
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, run := range w.running {
		if stale(run.key, id, want) {
			run.cancel()
			<-run.done
			delete(w.running, id)
		}
	}
	for id, c := range want {
		if _, ok := w.running[id]; !ok {
			w.running[id] = w.start(ctx, c)
		}
	}
	return nil
}

// watched picks the cameras that may report motion themselves: enabled and
// added over ONVIF.
func watched(cams []camera.Camera) map[camera.ID]camera.Camera {
	want := map[camera.ID]camera.Camera{}
	for _, c := range cams {
		if c.Enabled && c.Source.Kind == camera.SourceONVIF {
			want[c.ID] = c
		}
	}
	return want
}

// stale reports whether a running watch must stop: its camera is no longer
// wanted, or its address or credentials changed.
func stale(running watchKey, id camera.ID, want map[camera.ID]camera.Camera) bool {
	c, ok := want[id]
	return !ok || running != keyOf(c)
}

func keyOf(c camera.Camera) watchKey {
	return watchKey{address: c.Source.Address, creds: c.Credentials}
}

func (w *Watcher) start(ctx context.Context, c camera.Camera) *watch {
	ctx, cancel := context.WithCancel(ctx)
	run := &watch{key: keyOf(c), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		w.loop(ctx, c)
	}()
	return run
}

// loop subscribes again and again until ctx ends: soon after a broken
// subscription, rarely when the camera has no events at all.
func (w *Watcher) loop(ctx context.Context, c camera.Camera) {
	id := string(c.ID)
	log := w.opts.Logger.With("camera", c.ID)
	client := onvif.NewClient(c.Source.Address, onvif.Credentials{Username: c.Credentials.Username, Password: c.Credentials.Password})
	wait := time.Duration(0)
	for {
		live := false // until the camera says anything about motion, frames decide
		err := client.WatchMotion(ctx,
			func() { wait = 0 },
			func(on bool) {
				if !live {
					live = true
					w.hub.CameraSource(id, true)
					log.Info("motion: using the camera's own detector")
				}
				w.hub.CameraMotion(id, on, time.Now())
			})
		if live {
			w.hub.CameraSource(id, false)
		}
		if ctx.Err() != nil {
			return
		}
		wait = w.retryAfter(err, wait)
		log.Info("motion: camera events unavailable; reading frames", "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// retryAfter doubles the wait after a broken subscription, up to the cap,
// and waits long for a camera that has no events.
func (w *Watcher) retryAfter(err error, last time.Duration) time.Duration {
	if errors.Is(err, onvif.ErrNoEvents) {
		return w.opts.NoEvents
	}
	return min(max(2*last, w.opts.MinBackoff), w.opts.MaxBackoff)
}

func (w *Watcher) stopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, run := range w.running {
		run.cancel()
		<-run.done
		delete(w.running, id)
	}
}
