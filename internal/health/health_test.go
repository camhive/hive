package health_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/video"
)

// statusEngine answers Status from a table the test controls.
type statusEngine struct {
	video.Engine
	mu     sync.Mutex
	status map[string]video.Status
	err    map[string]error
}

func (e *statusEngine) set(cam, stream string, st video.Status, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status[cam+"/"+stream] = st
	if err != nil {
		e.err[cam+"/"+stream] = err
	} else {
		delete(e.err, cam+"/"+stream)
	}
}

func (e *statusEngine) Status(_ context.Context, cam video.CameraID, s video.Stream) (video.Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := string(cam) + "/" + string(s)
	if err, ok := e.err[k]; ok {
		return video.Status{}, err
	}
	return e.status[k], nil
}

type memStore struct {
	mu   sync.Mutex
	cams []camera.Camera
}

func (m *memStore) List(context.Context) ([]camera.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]camera.Camera(nil), m.cams...), nil
}

func cam(t *testing.T, name string) camera.Camera {
	t.Helper()
	c, err := camera.New(camera.Spec{Name: name, Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/" + name},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://h/" + name}, camera.Sub: {URL: "rtsp://h/" + name + "s"}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type recorder struct {
	mu     sync.Mutex
	events []health.Event
}

func (r *recorder) on(e health.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind + ":" + e.Camera
	}
	return out
}

func TestPollDerivesOnlineOfflineWithHysteresisAndBitrate(t *testing.T) {
	eng := &statusEngine{status: map[string]video.Status{}, err: map[string]error{}}
	c := cam(t, "a")
	st := &memStore{cams: []camera.Camera{c}}
	rec := &recorder{}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	m := health.New(st, eng, health.Options{OfflineAfter: 2, Now: func() time.Time { return now }, OnEvent: rec.on})
	ctx := context.Background()
	id := string(c.ID)

	// Unknown stream, first poll: not online yet, no event (startup is silent).
	eng.set(id, "main", video.Status{Ready: false}, nil)
	eng.set(id, "sub", video.Status{Ready: false}, nil)
	_ = m.Poll(ctx)
	if h := m.Camera(id); h.Online || len(rec.kinds()) != 0 {
		t.Fatalf("initial: %+v events=%v", h, rec.kinds())
	}

	// Frames flow: online at once, one event.
	eng.set(id, "main", video.Status{Ready: true, BytesReceived: 1_000_000, Tracks: []string{"H264"}}, nil)
	eng.set(id, "sub", video.Status{Ready: true, BytesReceived: 100_000}, nil)
	_ = m.Poll(ctx)
	h := m.Camera(id)
	if !h.Online || !h.Streams["main"].Online || h.Streams["main"].Tracks[0] != "H264" {
		t.Fatalf("online: %+v", h)
	}
	if got := rec.kinds(); len(got) != 1 || got[0] != "camera.online:"+id {
		t.Fatalf("events = %v", got)
	}

	// Ten seconds later, 1.25 MB more on main → 1000 kbit/s.
	now = now.Add(10 * time.Second)
	eng.set(id, "main", video.Status{Ready: true, BytesReceived: 2_250_000, Tracks: []string{"H264"}}, nil)
	_ = m.Poll(ctx)
	if kbps := m.Camera(id).Streams["main"].BitrateKbps; kbps != 1000 {
		t.Fatalf("bitrate = %d kbps, want 1000", kbps)
	}

	// One failed poll: still online (hysteresis). Second: offline, one event.
	now = now.Add(5 * time.Second)
	eng.set(id, "main", video.Status{}, errors.New("engine hiccup"))
	_ = m.Poll(ctx)
	if !m.Camera(id).Online {
		t.Fatal("one failure must not flip to offline")
	}
	now = now.Add(5 * time.Second)
	_ = m.Poll(ctx)
	h = m.Camera(id)
	if h.Online || !h.Streams["sub"].Online || h.Streams["main"].Online {
		t.Fatalf("after two failures: %+v", h)
	}
	if got := rec.kinds(); len(got) != 2 || got[1] != "camera.offline:"+id {
		t.Fatalf("events = %v", got)
	}
	if !h.Streams["main"].Since.Equal(now) {
		t.Fatalf("offline since = %v, want %v", h.Streams["main"].Since, now)
	}

	// Back: online again, third event. Polls are idempotent afterwards.
	now = now.Add(5 * time.Second)
	eng.set(id, "main", video.Status{Ready: true, BytesReceived: 3_000_000}, nil)
	_ = m.Poll(ctx)
	_ = m.Poll(ctx)
	if got := rec.kinds(); len(got) != 3 || got[2] != "camera.online:"+id {
		t.Fatalf("events = %v", got)
	}
}

func TestDisabledAndRemovedCamerasDropOutWithoutOfflineEvents(t *testing.T) {
	eng := &statusEngine{status: map[string]video.Status{}, err: map[string]error{}}
	a, b := cam(t, "a"), cam(t, "b")
	st := &memStore{cams: []camera.Camera{a, b}}
	rec := &recorder{}
	m := health.New(st, eng, health.Options{OfflineAfter: 2, OnEvent: rec.on})
	ctx := context.Background()
	for _, c := range []camera.Camera{a, b} {
		eng.set(string(c.ID), "main", video.Status{Ready: true}, nil)
		eng.set(string(c.ID), "sub", video.Status{Ready: true}, nil)
	}
	_ = m.Poll(ctx)
	if len(m.All()) != 2 {
		t.Fatalf("all = %+v", m.All())
	}
	e := false
	a2, _ := a.Update(camera.Patch{Enabled: &e}, time.Now())
	st.mu.Lock()
	st.cams = []camera.Camera{a2} // a disabled, b removed
	st.mu.Unlock()
	_ = m.Poll(ctx)
	all := m.All()
	if len(all) != 1 || all[0].Camera != string(a.ID) || all[0].Online || all[0].Enabled {
		t.Fatalf("after change = %+v", all)
	}
	if got := rec.kinds(); len(got) != 2 { // only the two camera.online events
		t.Fatalf("no offline events for disabled/removed cameras, got %v", got)
	}
	if h := m.Camera(string(b.ID)); h.Camera != "" {
		t.Fatalf("removed camera still reported: %+v", h)
	}
}

func TestRunPollsOnInterval(t *testing.T) {
	eng := &statusEngine{status: map[string]video.Status{}, err: map[string]error{}}
	c := cam(t, "a")
	st := &memStore{cams: []camera.Camera{c}}
	rec := &recorder{}
	m := health.New(st, eng, health.Options{Interval: 20 * time.Millisecond, OfflineAfter: 2, OnEvent: rec.on})
	eng.set(string(c.ID), "main", video.Status{Ready: true}, nil)
	eng.set(string(c.ID), "sub", video.Status{Ready: true}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(rec.kinds()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(rec.kinds()) == 0 {
		t.Fatal("Run never polled")
	}
}
