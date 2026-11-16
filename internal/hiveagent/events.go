package hiveagent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/camhive/hive/internal/capability"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/store"
)

// Event names the hive emits.
const (
	EventHeartbeat  = "hive.heartbeat"
	EventStorageLow = "storage.low"
)

// Heartbeat is the periodic health summary (ADR-0008).
type Heartbeat struct {
	Version    string   `json:"version"`
	BinaryHash string   `json:"binary_hash,omitempty"`
	Protocol   int      `json:"protocol"`
	UptimeS    int64    `json:"uptime_s"`
	Cameras    CamSum   `json:"cameras"`
	Disk       DiskSum  `json:"disk"`
	LAN        *LANInfo `json:"lan,omitempty"`
	License    string   `json:"license,omitempty"` // valid | grace | expired | none
	// Capabilities is what this build can do (ADR-0022).
	Capabilities []string  `json:"capabilities"`
	At           time.Time `json:"at"`
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
	hb := Heartbeat{Version: a.d.Version, BinaryHash: a.binHash, Protocol: a.d.Protocol, UptimeS: int64(now.Sub(a.started).Seconds()), At: now.UTC(),
		Capabilities: capability.Names()}
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
func (a *Agent) Emit(name string, data any) { a.enqueue(name, a.d.Now().UTC(), data) }

func (a *Agent) enqueue(name string, at time.Time, data any) {
	raw, _ := json.Marshal(data)
	a.mu.Lock()
	if len(a.queue) >= MaxQueuedEvents {
		a.queue = a.queue[1:]
	}
	a.seq++
	a.queue = append(a.queue, queued{seq: a.seq, e: protocol.Event{Name: name, At: at, Data: raw}})
	a.mu.Unlock()
	a.wake()
}

// eventData is what the cloud receives for a stored Event: its id on this
// Hive (with the Hive id, the key a notification is deduplicated on,
// ADR-0021), its Camera with the Camera's name (the cloud has no other
// copy of it) and its data.
func (a *Agent) eventData(e store.Event) map[string]any {
	m := map[string]any{"event_id": e.ID, "camera": e.Camera, "data": e.Data}
	if e.Camera != "" && a.d.Store != nil {
		if c, err := a.d.Store.Cameras().Get(context.Background(), camera.ID(e.Camera)); err == nil {
			m["camera_name"] = c.Name
		}
	}
	return m
}

// EmitEvent queues a stored Event for the cloud, stamped with its start.
func (a *Agent) EmitEvent(e store.Event) { a.enqueue(e.Kind, e.At.UTC(), a.eventData(e)) }

// EmitStart queues a lasting Event for the cloud when it begins; its end
// is not sent. Use it as an events.Observe callback.
func (a *Agent) EmitStart(e store.Event) {
	if e.End.IsZero() {
		a.EmitEvent(e)
	}
}

// PromptStart sends a lasting Event on the prompt lane when it begins (the
// start of motion); its end is not sent. Use it as an Observe callback.
func (a *Agent) PromptStart(e store.Event) {
	if e.End.IsZero() {
		a.enqueuePrompt(e.Kind, e.At.UTC(), a.eventData(e))
	}
}

// The prompt lane (ADR-0021) carries events that matter only while they are
// fresh, such as the start of motion: a few entries, each dropped when it
// is older than PromptTTL, never replayed after an outage, and never
// competing with status events for the queue's slots.
const (
	MaxPromptEvents = 32
	PromptTTL       = 60 * time.Second
)

// queued is an event waiting in a lane, numbered in the order it came.
type queued struct {
	seq uint64
	e   protocol.Event
}

type promptEvent struct {
	queued
	created time.Time // Now() with its monotonic reading, for the TTL
}

// EmitPrompt sends an event on the prompt lane: before anything queued,
// and only if a connection takes it within PromptTTL.
func (a *Agent) EmitPrompt(name string, data any) { a.enqueuePrompt(name, a.d.Now().UTC(), data) }

func (a *Agent) enqueuePrompt(name string, at time.Time, data any) {
	raw, _ := json.Marshal(data)
	now := a.d.Now()
	a.mu.Lock()
	if len(a.prompt) >= MaxPromptEvents {
		a.prompt = a.prompt[1:]
	}
	a.seq++
	a.prompt = append(a.prompt, promptEvent{queued: queued{seq: a.seq, e: protocol.Event{Name: name, At: at, Data: raw}}, created: now})
	a.mu.Unlock()
	a.wake()
}

func (a *Agent) wake() {
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

// flush sends the prompt lane, then the queue, in order; on the first
// failure it stops and arms a short retry so a reconnect drains both
// promptly.
func (a *Agent) flush(ctx context.Context, emit Emitter, retry *time.Timer) {
	for {
		q, fromPrompt, ok := a.next()
		if !ok {
			return
		}
		if err := emit(ctx, q.e); err != nil {
			retry.Reset(2 * time.Second)
			return
		}
		a.sent(fromPrompt, q.seq)
	}
}

// sent pops the head of a lane if it is still the event that was sent; an
// overflow while it was on the wire may have dropped it already, and then
// the new head was never sent.
func (a *Agent) sent(fromPrompt bool, seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fromPrompt && len(a.prompt) > 0 && a.prompt[0].seq == seq {
		a.prompt = a.prompt[1:]
	}
	if !fromPrompt && len(a.queue) > 0 && a.queue[0].seq == seq {
		a.queue = a.queue[1:]
	}
}

// next returns the event to send next: the oldest fresh prompt event
// (expired ones are dropped on the way), else the head of the queue.
func (a *Agent) next() (queued, bool, bool) {
	now := a.d.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for len(a.prompt) > 0 && now.Sub(a.prompt[0].created) > PromptTTL {
		a.prompt = a.prompt[1:]
	}
	if len(a.prompt) > 0 {
		return a.prompt[0].queued, true, true
	}
	if len(a.queue) > 0 {
		return a.queue[0], false, true
	}
	return queued{}, false, false
}
