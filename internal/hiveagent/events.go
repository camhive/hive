package hiveagent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/camhive/hive/internal/protocol"
)

// Event names the hive emits.
const (
	EventHeartbeat  = "hive.heartbeat"
	EventStorageLow = "storage.low"
)

// Heartbeat is the periodic health summary (ADR-0008).
type Heartbeat struct {
	Version    string    `json:"version"`
	BinaryHash string    `json:"binary_hash,omitempty"`
	Protocol   int       `json:"protocol"`
	UptimeS    int64     `json:"uptime_s"`
	Cameras    CamSum    `json:"cameras"`
	Disk       DiskSum   `json:"disk"`
	LAN        *LANInfo  `json:"lan,omitempty"`
	License    string    `json:"license,omitempty"` // valid | grace | expired | none
	At         time.Time `json:"at"`
}

// CamSum counts cameras.
type CamSum struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
	Online  int `json:"online"`
}

// DiskSum is the archive volume state.
type DiskSum struct {
	FreeBytes int64 `json:"free_bytes"`
	Known     bool  `json:"known"`
}

// Heartbeat builds the current summary.
func (a *Agent) Heartbeat() Heartbeat {
	now := a.d.Now()
	hb := Heartbeat{Version: a.d.Version, BinaryHash: a.binHash, Protocol: a.d.Protocol, UptimeS: int64(now.Sub(a.started).Seconds()), At: now.UTC()}
	if h := a.healthSource(); h != nil {
		for _, c := range h.All() {
			hb.Cameras.Total++
			if c.Enabled {
				hb.Cameras.Enabled++
			}
			if c.Online {
				hb.Cameras.Online++
			}
		}
	}
	if a.d.Free != nil {
		if free, err := a.d.Free(); err == nil {
			hb.Disk = DiskSum{FreeBytes: free, Known: true}
		}
	}
	hb.LAN = a.lanInfo()
	if a.d.License != nil {
		hb.License = string(a.d.License.State())
	}
	return hb
}

// Emit queues an event for the cloud; Run delivers it when connected.
// Events are the hive's own vocabulary (camera.online, storage.low, …).
func (a *Agent) Emit(name string, data any) {
	raw, _ := json.Marshal(data)
	a.mu.Lock()
	if len(a.queue) >= MaxQueuedEvents {
		a.queue = a.queue[1:]
	}
	a.queue = append(a.queue, protocol.Event{Name: name, At: a.d.Now().UTC(), Data: raw})
	a.mu.Unlock()
	select {
	case a.notify <- struct{}{}:
	default:
	}
}

// Pending returns the number of events waiting for delivery.
func (a *Agent) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queue)
}

// Emitter sends one event to the cloud; hivelink supplies it.
type Emitter func(ctx context.Context, e protocol.Event) error

// Run sends a heartbeat every interval and flushes queued events as soon
// as the emitter accepts them (it fails while disconnected; the queue
// keeps them). Returns when ctx is done.
func (a *Agent) Run(ctx context.Context, emit Emitter, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			raw, _ := json.Marshal(a.Heartbeat())
			if err := emit(ctx, protocol.Event{Name: EventHeartbeat, At: a.d.Now().UTC(), Data: raw}); err != nil {
				a.log.Debug("heartbeat not sent", "err", err)
			}
			a.flush(ctx, emit, retry)
		case <-a.notify:
			a.flush(ctx, emit, retry)
		case <-retry.C:
			a.flush(ctx, emit, retry)
		}
	}
}

// flush sends queued events in order; on the first failure it stops and
// arms a short retry so a reconnect drains the queue promptly.
func (a *Agent) flush(ctx context.Context, emit Emitter, retry *time.Timer) {
	for {
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			return
		}
		e := a.queue[0]
		a.mu.Unlock()
		if err := emit(ctx, e); err != nil {
			retry.Reset(2 * time.Second)
			return
		}
		a.mu.Lock()
		a.queue = a.queue[1:]
		a.mu.Unlock()
	}
}
