// Package health watches camera streams through the video engine and turns
// raw status into something a person can act on: online/offline per stream
// and per camera with hysteresis (one bad poll is noise), bitrate from byte
// deltas, and camera.online / camera.offline events for the timeline and
// the cloud.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/video"
)

// CameraLister is the part of the store health needs.
type CameraLister interface {
	List(ctx context.Context) ([]camera.Camera, error)
}

// Event is a status change.
type Event struct {
	Kind   string // camera.online | camera.offline
	Camera string
	At     time.Time
	Data   json.RawMessage // {"stream": "main"} for the stream that flipped
}

// StreamHealth is the state of one stream.
type StreamHealth struct {
	Stream        string    `json:"stream"`
	Online        bool      `json:"online"`
	Since         time.Time `json:"since"` // when the current state began
	BitrateKbps   int64     `json:"bitrate_kbps"`
	BytesReceived int64     `json:"bytes_received"`
	Tracks        []string  `json:"tracks"`
	Readers       int       `json:"readers"`
	LastError     string    `json:"last_error,omitempty"`
}

// CameraHealth is the state of one camera: online when its main stream is.
type CameraHealth struct {
	Camera  string                  `json:"camera"`
	Enabled bool                    `json:"enabled"`
	Online  bool                    `json:"online"`
	Streams map[string]StreamHealth `json:"streams"`
}

// Options tune the monitor.
type Options struct {
	Interval     time.Duration // poll period; default 5s
	OfflineAfter int           // consecutive bad polls before offline; default 2
	Now          func() time.Time
	OnEvent      func(Event)
	Logger       *slog.Logger
}

// Monitor polls and remembers.
type Monitor struct {
	store  CameraLister
	engine video.Engine
	opts   Options

	mu   sync.RWMutex
	cams map[string]*camState
}

type camState struct {
	enabled bool
	streams map[string]*streamState
}

type streamState struct {
	StreamHealth
	failures   int
	lastBytes  int64
	lastSample time.Time
}

// New creates a Monitor.
func New(store CameraLister, engine video.Engine, opts Options) *Monitor {
	if opts.Interval == 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.OfflineAfter == 0 {
		opts.OfflineAfter = 2
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.OnEvent == nil {
		opts.OnEvent = func(Event) {}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Monitor{store: store, engine: engine, opts: opts, cams: map[string]*camState{}}
}

// Run polls every Interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(m.opts.Interval)
	defer t.Stop()
	for {
		if err := m.Poll(ctx); err != nil {
			m.opts.Logger.Warn("health poll", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll reads every enabled camera's streams once and updates state.
func (m *Monitor) Poll(ctx context.Context) error {
	cams, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	now := m.opts.Now()
	var events []Event
	m.mu.Lock()
	seen := map[string]bool{}
	for _, c := range cams {
		id := string(c.ID)
		seen[id] = true
		cs := m.cams[id]
		if cs == nil {
			cs = &camState{streams: map[string]*streamState{}}
			m.cams[id] = cs
		}
		cs.enabled = c.Enabled
		if !c.Enabled {
			cs.streams = map[string]*streamState{} // silent: disabling is not an outage
			continue
		}
		for name := range c.Streams {
			events = append(events, m.pollStream(ctx, cs, id, string(name), now)...)
		}
	}
	for id := range m.cams {
		if !seen[id] {
			delete(m.cams, id)
		}
	}
	m.mu.Unlock()
	for _, e := range events {
		m.opts.OnEvent(e)
	}
	return nil
}

// pollStream updates one stream and returns the events it caused.
func (m *Monitor) pollStream(ctx context.Context, cs *camState, cam, stream string, now time.Time) []Event {
	ss := cs.streams[stream]
	if ss == nil {
		ss = &streamState{StreamHealth: StreamHealth{Stream: stream, Since: now}}
		cs.streams[stream] = ss
	}
	st, err := m.engine.Status(ctx, video.CameraID(cam), video.Stream(stream))
	ok := err == nil && st.Ready
	if err != nil {
		ss.LastError = err.Error()
	} else {
		ss.LastError = ""
	}
	// Camera-level events follow the main stream; sub streams change silently.
	if ok {
		ss.failures = 0
		ss.updateCounters(st, now)
		if !ss.Online {
			ss.Online, ss.Since = true, now
			if stream == "main" {
				return []Event{{Kind: "camera.online", Camera: cam, At: now, Data: streamData(stream)}}
			}
		}
		return nil
	}
	ss.failures++
	if ss.Online && ss.failures >= m.opts.OfflineAfter {
		ss.Online, ss.Since = false, now
		ss.BitrateKbps = 0
		if stream == "main" {
			return []Event{{Kind: "camera.offline", Camera: cam, At: now, Data: streamData(stream)}}
		}
	}
	return nil
}

func (ss *streamState) updateCounters(st video.Status, now time.Time) {
	if !ss.lastSample.IsZero() && st.BytesReceived >= ss.lastBytes {
		if dt := now.Sub(ss.lastSample).Seconds(); dt > 0 {
			ss.BitrateKbps = int64(float64(st.BytesReceived-ss.lastBytes) * 8 / 1000 / dt)
		}
	}
	ss.lastBytes, ss.lastSample = st.BytesReceived, now
	ss.BytesReceived, ss.Tracks, ss.Readers = st.BytesReceived, st.Tracks, st.Readers
}

func streamData(stream string) json.RawMessage {
	d, _ := json.Marshal(map[string]string{"stream": stream})
	return d
}

// Camera returns a camera's health; zero value when unknown.
func (m *Monitor) Camera(id string) CameraHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cs, ok := m.cams[id]
	if !ok {
		return CameraHealth{}
	}
	return cs.health(id)
}

// All returns every known camera's health, sorted by id.
func (m *Monitor) All() []CameraHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CameraHealth, 0, len(m.cams))
	for id, cs := range m.cams {
		out = append(out, cs.health(id))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Camera < out[b].Camera })
	return out
}

func (cs *camState) health(id string) CameraHealth {
	h := CameraHealth{Camera: id, Enabled: cs.enabled, Streams: make(map[string]StreamHealth, len(cs.streams))}
	for name, ss := range cs.streams {
		h.Streams[name] = ss.StreamHealth
	}
	if main, ok := cs.streams["main"]; ok {
		h.Online = main.Online
	}
	return h
}
