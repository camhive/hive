package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/events"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/onvif"
	"github.com/camhive/hive/internal/store"
)

// careEvery is how often camera care judges every camera (#163).
const careEvery = 5 * time.Minute

// degradedEpisodes records camera care's episodes (#163): one lasting
// camera.degraded Event per episode, sent to the cloud when it begins,
// and camera.recovered when it ends.
type degradedEpisodes struct {
	sink events.Sink
	send func(store.Event) // to the cloud (hiveagent.Agent.EmitEvent)
	log  *slog.Logger

	mu   sync.Mutex
	open map[string]*events.Condition
}

func newDegradedEpisodes(sink events.Sink, send func(store.Event), log *slog.Logger) *degradedEpisodes {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &degradedEpisodes{sink: sink, send: send, log: log, open: map[string]*events.Condition{}}
}

// begin records the episode that began at since; care calls it on every
// check while the Camera is degraded, so a failed write is retried (the
// Condition does nothing while its Event is open).
func (d *degradedEpisodes) begin(cam string, since time.Time, reasons []health.Reason) {
	d.mu.Lock()
	c := d.open[cam]
	if c == nil {
		c = events.NewCondition(d.sink, cam, health.EventDegraded, events.WithLogger(d.log), events.Observe(func(e store.Event) {
			if e.End.IsZero() {
				d.send(e)
			}
		}))
		d.open[cam] = c
	}
	d.mu.Unlock()
	c.Begin(context.Background(), since, map[string]any{"reasons": reasons})
}

// end closes the episode; recovered sends camera.recovered (not when the
// owner disabled or removed the camera).
func (d *degradedEpisodes) end(cam string, at time.Time, recovered bool) {
	d.mu.Lock()
	c := d.open[cam]
	delete(d.open, cam)
	d.mu.Unlock()
	if c == nil {
		return
	}
	c.End(context.Background(), at)
	if recovered {
		d.send(store.Event{Camera: cam, Kind: health.EventRecovered, At: at.UTC()})
	}
}

// cameraClock reads an ONVIF camera's clock for camera care.
func cameraClock(ctx context.Context, c camera.Camera) (time.Time, error) {
	return onvif.NewClient(c.Source.Address, onvif.Credentials{Username: c.Credentials.Username, Password: c.Credentials.Password}).SystemDateAndTime(ctx)
}
