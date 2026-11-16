// Package events records lasting conditions on the Hive's timeline
// (ADR-0020): motion on a Camera, low storage on the Hive. A Condition is one
// Event from when it begins to when it ends; its row id is known from the
// start, so an observer (notifications, #66) can act on the beginning and
// update on the end.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/camhive/hive/internal/store"
)

// Sink stores lasting Events; *store.Events is one.
type Sink interface {
	Begin(ctx context.Context, e store.Event) (int64, error)
	End(ctx context.Context, id int64, end time.Time) error
}

// Option customises a Condition.
type Option func(*Condition)

// Observe calls fn with the stored Event when the Condition begins (End
// zero) and when it ends. fn runs on the caller's goroutine; keep it short.
func Observe(fn func(store.Event)) Option { return func(c *Condition) { c.observe = fn } }

// WithLogger sets the logger for storage errors; the default discards.
func WithLogger(l *slog.Logger) Option { return func(c *Condition) { c.log = l } }

// Condition is one kind of lasting Event of one Camera ("" for the Hive).
// Safe for concurrent use.
type Condition struct {
	sink    Sink
	camera  string
	kind    string
	observe func(store.Event)
	log     *slog.Logger

	mu   sync.Mutex
	open store.Event // ID 0 when nothing is open
}

// NewCondition creates a Condition that is not on.
func NewCondition(sink Sink, camera, kind string, opts ...Option) *Condition {
	c := &Condition{sink: sink, camera: camera, kind: kind, observe: func(store.Event) {}, log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Begin records the start of an episode at `at` with data; while one is
// open it does nothing. A failed write is logged and retried on the next Begin.
func (c *Condition) Begin(ctx context.Context, at time.Time, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open.ID != 0 {
		return
	}
	var raw json.RawMessage
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	e := store.Event{Camera: c.camera, Kind: c.kind, At: at.UTC(), Data: raw}
	id, err := c.sink.Begin(ctx, e)
	if err != nil {
		c.log.Error("store event", "kind", c.kind, "camera", c.camera, "err", err)
		return
	}
	e.ID = id
	c.open = e
	c.observe(e)
}

// End closes the open episode at `at`; with none open it does nothing. A
// failed write is logged and the episode stays open for the next End.
func (c *Condition) End(ctx context.Context, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open.ID == 0 {
		return
	}
	if err := c.sink.End(ctx, c.open.ID, at.UTC()); err != nil {
		c.log.Error("end event", "kind", c.kind, "camera", c.camera, "err", err)
		return
	}
	e := c.open
	e.End = at.UTC()
	c.open = store.Event{}
	c.observe(e)
}
