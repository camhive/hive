package hiveagent_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/tunnel"
)

// cloudLink is an Emitter whose connection the test switches.
type cloudLink struct {
	mu        sync.Mutex
	connected bool
	got       []protocol.Event
}

func (l *cloudLink) emit(_ context.Context, e protocol.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.connected {
		return tunnel.ErrNotConnected
	}
	l.got = append(l.got, e)
	return nil
}

func (l *cloudLink) connect() {
	l.mu.Lock()
	l.connected = true
	l.mu.Unlock()
}

// names returns the names of delivered events other than heartbeats.
func (l *cloudLink) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, e := range l.got {
		if e.Name != hiveagent.EventHeartbeat {
			out = append(out, e.Name)
		}
	}
	return out
}

// fakeClock is a settable Now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func waitNames(t *testing.T, l *cloudLink, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := l.names(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivered %v, want %d events", l.names(), n)
	return nil
}

func startAgent(t *testing.T, clock *fakeClock, l *cloudLink) *hiveagent.Agent {
	t.Helper()
	ag := hiveagent.New(hiveagent.Deps{Now: clock.now})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ag.Run(ctx, l.emit, time.Hour)
	return ag
}

func TestAPromptEventGoesOutBeforeQueuedStatusEvents(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := &cloudLink{}
	ag := startAgent(t, clock, l)
	ag.Emit("camera.offline", map[string]string{"camera": "a"})
	ag.EmitPrompt("motion", map[string]any{"camera": "b", "event_id": 7})
	l.connect()
	ag.Emit("camera.online", map[string]string{"camera": "a"}) // wakes the loop

	got := waitNames(t, l, 3)
	if got[0] != "motion" || got[1] != "camera.offline" || got[2] != "camera.online" {
		t.Fatalf("order = %v", got)
	}
	var data map[string]any
	_ = json.Unmarshal(l.got[0].Data, &data)
	if data["event_id"] != float64(7) || !l.got[0].At.Equal(clock.now()) {
		t.Fatalf("motion event = %+v", l.got[0])
	}
}

func TestAPromptEventOlderThanItsTTLIsDroppedNotReplayed(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := &cloudLink{}
	ag := startAgent(t, clock, l)
	ag.EmitPrompt("motion", map[string]any{"camera": "b", "event_id": 1})
	ag.Emit("camera.offline", map[string]string{"camera": "a"})
	clock.advance(hiveagent.PromptTTL + time.Second)
	ag.EmitPrompt("motion", map[string]any{"camera": "b", "event_id": 2})
	l.connect()
	ag.Emit("camera.online", map[string]string{"camera": "a"})

	waitNames(t, l, 3)
	time.Sleep(50 * time.Millisecond) // nothing else may follow
	got := l.names()
	if len(got) != 3 || got[0] != "motion" || got[1] != "camera.offline" || got[2] != "camera.online" {
		t.Fatalf("delivered = %v", got)
	}
	var data map[string]any
	_ = json.Unmarshal(l.got[0].Data, &data)
	if data["event_id"] != float64(2) {
		t.Fatalf("the expired motion event was sent: %s", l.got[0].Data)
	}
}

func TestThePromptLaneKeepsOnlyTheNewestEntries(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := &cloudLink{}
	ag := startAgent(t, clock, l)
	for i := range hiveagent.MaxPromptEvents + 5 {
		ag.EmitPrompt("motion", map[string]any{"event_id": i})
	}
	if ag.Pending() != 0 {
		t.Fatalf("prompt events counted in the status queue: %d", ag.Pending())
	}
	l.connect()
	ag.Emit("camera.online", nil)

	got := waitNames(t, l, hiveagent.MaxPromptEvents+1)
	time.Sleep(50 * time.Millisecond)
	if n := len(l.names()); n != hiveagent.MaxPromptEvents+1 {
		t.Fatalf("delivered %d events: %v", n, got)
	}
	var first map[string]any
	_ = json.Unmarshal(l.got[0].Data, &first)
	if first["event_id"] != float64(5) {
		t.Fatalf("oldest kept = %v, want 5", first["event_id"])
	}
}

func TestStoredEventsReachTheCloudWithTheirIDAndMotionOnlyAtItsStart(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := &cloudLink{}
	ag := startAgent(t, clock, l)
	began := time.Date(2026, 11, 16, 11, 59, 58, 0, time.UTC)
	ag.EmitEvent(store.Event{ID: 41, Camera: "a", Kind: "camera.offline", At: began, End: began})
	motion := store.Event{ID: 42, Camera: "b", Kind: "motion", At: began, Data: json.RawMessage(`{"source":"frames"}`)}
	ag.PromptStart(motion)
	motion.End = began.Add(20 * time.Second)
	ag.PromptStart(motion) // the end of the episode is not sent
	low := store.Event{ID: 43, Kind: "storage.low", At: began, Data: json.RawMessage(`{"free_bytes":1}`)}
	ag.EmitStart(low)
	low.End = began.Add(time.Hour)
	ag.EmitStart(low)
	l.connect()
	ag.Emit("camera.online", nil)

	waitNames(t, l, 4)
	time.Sleep(50 * time.Millisecond) // nothing else may follow
	if got := l.names(); len(got) != 4 || got[0] != "motion" || got[1] != "camera.offline" || got[2] != "storage.low" {
		t.Fatalf("delivered = %v", got)
	}
	type payload struct {
		EventID int64           `json:"event_id"`
		Camera  string          `json:"camera"`
		Data    json.RawMessage `json:"data"`
	}
	want := []payload{{42, "b", json.RawMessage(`{"source":"frames"}`)}, {41, "a", nil}, {43, "", json.RawMessage(`{"free_bytes":1}`)}}
	for i, w := range want {
		var p payload
		if err := json.Unmarshal(l.got[i].Data, &p); err != nil {
			t.Fatal(err)
		}
		if p.EventID != w.EventID || p.Camera != w.Camera || string(p.Data) != string(w.Data) && w.Data != nil {
			t.Fatalf("event %d = %s, want %+v", i, l.got[i].Data, w)
		}
		if !l.got[i].At.Equal(began) {
			t.Fatalf("event %d at %v, want the Event's start %v", i, l.got[i].At, began)
		}
	}
}

// The cloud knows no camera names; a notification needs one.
func TestACameraEventCarriesTheCamerasName(t *testing.T) {
	ag, st, _, _ := newAgent(t)
	cam, _ := camera.New(camera.Spec{Name: "Front door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://x/m"}}, time.Now())
	if err := st.Cameras().Put(t.Context(), cam); err != nil {
		t.Fatal(err)
	}
	l := &cloudLink{connected: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx, l.emit, time.Hour)
	ag.PromptStart(store.Event{ID: 1, Camera: string(cam.ID), Kind: "motion", At: time.Now()})
	ag.EmitEvent(store.Event{ID: 2, Camera: "gone", Kind: "camera.offline", At: time.Now()})
	waitNames(t, l, 2)
	var withName, without map[string]any
	_ = json.Unmarshal(l.got[0].Data, &withName)
	_ = json.Unmarshal(l.got[1].Data, &without)
	if withName["camera_name"] != "Front door" || without["camera_name"] != nil {
		t.Fatalf("payloads = %s, %s", l.got[0].Data, l.got[1].Data)
	}
}

// Review of #108 item 7: while the head of a lane is being sent, new events
// overflow the lane and drop that head. The entry after it was never
// sent and must not be popped in its place.
func TestAnEventBehindAHeadDroppedDuringItsSendIsNotLost(t *testing.T) {
	for _, lane := range []struct {
		name string
		cap  int
		emit func(*hiveagent.Agent, int)
	}{
		{"prompt", hiveagent.MaxPromptEvents, func(a *hiveagent.Agent, i int) { a.EmitPrompt("motion", map[string]int{"n": i}) }},
		{"queue", hiveagent.MaxQueuedEvents, func(a *hiveagent.Agent, i int) { a.Emit("camera.offline", map[string]int{"n": i}) }},
	} {
		t.Run(lane.name, func(t *testing.T) {
			clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
			ag := hiveagent.New(hiveagent.Deps{Now: clock.now})
			var mu sync.Mutex
			var got []int
			first := true
			emit := func(_ context.Context, e protocol.Event) error {
				var d struct{ N int }
				_ = json.Unmarshal(e.Data, &d)
				mu.Lock()
				got = append(got, d.N)
				overflow := first
				first = false
				mu.Unlock()
				if overflow { // fill the lane behind the head while the head is "on the wire"
					for i := 2; i <= lane.cap; i++ {
						lane.emit(ag, 100+i)
					}
				}
				return nil
			}
			lane.emit(ag, 0)
			lane.emit(ag, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go ag.Run(ctx, emit, time.Hour)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				mu.Lock()
				n := len(got)
				mu.Unlock()
				if n >= lane.cap {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) < 2 || got[0] != 0 || got[1] != 1 {
				t.Fatalf("sent %v…, want 0 then 1", got[:min(len(got), 3)])
			}
		})
	}
}
