// Package ingest keeps the video engine in step with the camera store: every
// enabled camera's streams are engine sources, nothing else is. It also
// supervises the engine process, restarting it and re-adding sources when it
// dies. Reconcile is idempotent and runs periodically, on demand (Notify)
// and after an engine restart.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/video"
)

// CameraLister is the part of the store ingest needs.
type CameraLister interface {
	List(ctx context.Context) ([]camera.Camera, error)
}

// Supervised is implemented by engines whose process can die; Exited is
// closed when the current process has exited.
type Supervised interface {
	Exited() <-chan struct{}
}

// Options tune the loop.
type Options struct {
	Interval       time.Duration // periodic reconcile; default 30s
	RestartBackoff time.Duration // wait before restarting a dead engine; default 2s
	Logger         *slog.Logger
}

// Ingest is the reconciler.
type Ingest struct {
	store  CameraLister
	engine video.Engine
	opts   Options
	log    *slog.Logger

	mu     sync.Mutex
	actual map[sourceKey]video.Source // what we believe the engine has
	kick   chan struct{}
}

type sourceKey struct {
	cam    video.CameraID
	stream video.Stream
}

// New creates an Ingest; Run or Reconcile drive it.
func New(store CameraLister, engine video.Engine, opts Options) *Ingest {
	if opts.Interval == 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.RestartBackoff == 0 {
		opts.RestartBackoff = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Ingest{store: store, engine: engine, opts: opts, log: opts.Logger, actual: map[sourceKey]video.Source{}, kick: make(chan struct{}, 1)}
}

// Notify asks the running loop to reconcile now (e.g. after a camera change).
func (i *Ingest) Notify() {
	select {
	case i.kick <- struct{}{}:
	default:
	}
}

// Reconcile makes the engine's sources equal the desired set. Errors from
// individual sources are collected; the rest of the set is still applied.
func (i *Ingest) Reconcile(ctx context.Context) error {
	cams, err := i.store.List(ctx)
	if err != nil {
		return fmt.Errorf("ingest: list cameras: %w", err)
	}
	desired := desiredSources(cams)

	i.mu.Lock()
	defer i.mu.Unlock()
	var errs []error
	for key, src := range desired {
		if cur, ok := i.actual[key]; ok && cur == src {
			continue
		}
		if err := i.engine.AddSource(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("ingest: add %s/%s: %w", key.cam, key.stream, err))
			continue
		}
		i.actual[key] = src
		i.log.Info("source added", "camera", key.cam, "stream", key.stream)
	}
	for key := range i.actual {
		if _, ok := desired[key]; ok {
			continue
		}
		if err := i.engine.RemoveSource(ctx, key.cam, key.stream); err != nil {
			errs = append(errs, fmt.Errorf("ingest: remove %s/%s: %w", key.cam, key.stream, err))
			continue
		}
		delete(i.actual, key)
		i.log.Info("source removed", "camera", key.cam, "stream", key.stream)
	}
	return errors.Join(errs...)
}

// desiredSources maps enabled cameras with known streams to engine sources,
// with credentials placed in the URL as the engine's RTSP client expects.
func desiredSources(cams []camera.Camera) map[sourceKey]video.Source {
	out := map[sourceKey]video.Source{}
	for _, c := range cams {
		if !c.Enabled {
			continue
		}
		for name, s := range c.Streams {
			if s.URL == "" {
				continue
			}
			key := sourceKey{cam: video.CameraID(c.ID), stream: video.Stream(name)}
			out[key] = video.Source{Camera: key.cam, Stream: key.stream, URL: withCredentials(s.URL, c.Credentials)}
		}
	}
	return out
}

func withCredentials(raw string, creds camera.Credentials) string {
	if creds.Username == "" && creds.Password == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword(creds.Username, creds.Password)
	return u.String()
}

// Run starts the engine and loops until ctx is cancelled: reconcile on start,
// every Interval, on Notify, and after restarting a dead engine.
func (i *Ingest) Run(ctx context.Context) error {
	if err := i.startEngine(ctx); err != nil {
		return err
	}
	defer func() { _ = i.engine.Stop() }()

	ticker := time.NewTicker(i.opts.Interval)
	defer ticker.Stop()
	for {
		if err := i.Reconcile(ctx); err != nil {
			i.log.Warn("reconcile", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-i.kick:
		case <-i.exited():
			i.log.Error("video engine exited; restarting", "backoff", i.opts.RestartBackoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(i.opts.RestartBackoff):
			}
			if err := i.startEngine(ctx); err != nil {
				i.log.Error("video engine restart failed", "err", err)
			}
		}
	}
}

// startEngine (re)starts the engine and forgets what it had: a fresh process
// has no sources, so the next Reconcile re-adds everything.
func (i *Ingest) startEngine(ctx context.Context) error {
	_ = i.engine.Stop()
	if err := i.engine.Start(ctx); err != nil {
		return fmt.Errorf("ingest: start engine: %w", err)
	}
	i.mu.Lock()
	i.actual = map[sourceKey]video.Source{}
	i.mu.Unlock()
	return nil
}

// exited returns the engine's exit channel, or one that never fires.
func (i *Ingest) exited() <-chan struct{} {
	if s, ok := i.engine.(Supervised); ok {
		return s.Exited()
	}
	return nil
}
