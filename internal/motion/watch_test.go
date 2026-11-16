package motion_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/store"
)

// An ONVIF camera with an event service is the camera source: the watcher
// subscribes, and the camera's own motion (moving 2–3 s of every 3 s on the
// mock) becomes motion Events marked as coming from the camera.
func TestTheWatcherTurnsTheCamerasOwnMotionIntoEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h, _ := camsim.ONVIFHandler(camsim.Config{Username: "admin", Password: "pw",
		Cameras: []camsim.Camera{{Name: "gate", Codec: camsim.H264, Motion: 3 * time.Second}}})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 { // the camera is still booting: the first attempt fails and is retried
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cam, err := camera.New(camera.Spec{Name: "Gate", Source: camera.Source{Kind: camera.SourceONVIF, Address: srv.URL + "/onvif/device_service"},
		Credentials: camera.Credentials{Username: "admin", Password: "pw"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rtsp, _ := camera.New(camera.Spec{Name: "Plain", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://127.0.0.1:1/x"}}, time.Now())
	for _, c := range []camera.Camera{cam, rtsp} {
		if err := st.Cameras().Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	cfg := motion.Defaults()
	cfg.Hold = 300 * time.Millisecond
	ended := make(chan store.Event, 8)
	hub := motion.NewHub(st.Events(), motion.Options{Config: cfg, Tick: 50 * time.Millisecond, Observe: func(e store.Event) {
		if !e.End.IsZero() {
			ended <- e
		}
	}})
	go hub.Run(ctx)
	w := motion.NewWatcher(st.Cameras(), hub, motion.WatchOptions{MinBackoff: 50 * time.Millisecond})
	go w.Run(ctx)

	var e store.Event
	select {
	case e = <-ended:
	case <-ctx.Done():
		t.Fatal("no motion event from the camera")
	}
	if d := e.End.Sub(e.At); e.Camera != string(cam.ID) || d < 500*time.Millisecond || d > 2*time.Second {
		t.Fatalf("event %+v lasted %v; the object moves for 1 s", e, d)
	}
	rows, _ := st.Events().Range(ctx, string(cam.ID), e.At.Add(-time.Second), e.At.Add(time.Second))
	var data struct{ Source string }
	if len(rows) == 0 || json.Unmarshal(rows[0].Data, &data) != nil || data.Source != string(motion.SourceCamera) {
		t.Fatalf("rows %+v", rows)
	}
	if w.Watching() != 1 {
		t.Fatalf("watching %d cameras, want only the ONVIF one", w.Watching())
	}

	// Switching the camera off ends its subscription.
	off := false
	cam, _ = cam.Update(camera.Patch{Enabled: &off}, time.Now())
	if err := st.Cameras().Put(ctx, cam); err != nil {
		t.Fatal(err)
	}
	w.Notify()
	for w.Watching() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("the disabled camera is still watched")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type reporter struct {
	mu   sync.Mutex
	live []bool
}

func (r *reporter) CameraSource(_ string, live bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = append(r.live, live)
}
func (r *reporter) CameraMotion(string, bool, time.Time) {}
func (r *reporter) sources() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.live...)
}

// A camera with an event service but motion detection off (a common
// default) never mentions motion: its subscription must not silence the
// frames detector.
func TestASubscriptionThatNeverMentionsMotionLeavesFramesOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h, _ := camsim.ONVIFHandler(camsim.Config{Cameras: []camsim.Camera{{Name: "door", Codec: camsim.H264}}}) // events, no :motion
	subscribed := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/onvif/events_service" {
			subscribed <- struct{}{}
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceONVIF, Address: srv.URL + "/onvif/device_service"}}, time.Now())
	var r reporter
	w := motion.NewWatcher(fixedCameras{cam}, &r, motion.WatchOptions{})
	go w.Run(ctx)
	select {
	case <-subscribed:
	case <-ctx.Done():
		t.Fatal("never subscribed")
	}
	time.Sleep(700 * time.Millisecond) // the first pull has come back empty
	for _, live := range r.sources() {
		if live {
			t.Fatalf("the camera source went live without a motion message: %v", r.sources())
		}
	}
}

type fixedCameras []camera.Camera

func (f fixedCameras) List(context.Context) ([]camera.Camera, error) { return f, nil }
