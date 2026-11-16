package motion_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/store"
)

func TestTheHubRecordsTheCamerasMotionAsOneEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := motion.Defaults()
	cfg.Hold = 50 * time.Millisecond
	seen := make(chan store.Event, 4)
	hub := motion.NewHub(st.Events(), motion.Options{Config: cfg, Tick: 10 * time.Millisecond, Observe: func(e store.Event) { seen <- e }})
	done := make(chan struct{})
	go func() { hub.Run(ctx); close(done) }()

	start := time.Now().UTC()
	hub.CameraSource("cam_a", true)
	hub.CameraMotion("cam_a", true, start)
	next := func() store.Event {
		select {
		case e := <-seen:
			return e
		case <-time.After(2 * time.Second): // the 50 ms hold given, not the default 10 s
			t.Fatal("no event within 2 s")
			return store.Event{}
		}
	}
	began := next()
	hub.CameraMotion("cam_a", false, start.Add(20*time.Millisecond))
	ended := next()
	if began.ID == 0 || ended.ID != began.ID || !began.End.IsZero() || ended.End.IsZero() {
		t.Fatalf("observer: began %+v ended %+v", began, ended)
	}
	rows, _ := st.Events().Range(ctx, "cam_a", start.Add(-time.Minute), start.Add(time.Minute))
	var data struct{ Source string }
	if len(rows) != 1 || rows[0].Kind != motion.Kind || rows[0].End.IsZero() || json.Unmarshal(rows[0].Data, &data) != nil || data.Source != "camera" {
		t.Fatalf("rows %+v", rows)
	}
	cancel()
	<-done
}

// A clean stop (service restart, update) ends open episodes at their last
// activity instead of leaving them for the crash cleanup, which would make
// them zero-length.
func TestStoppingTheHubEndsOpenEpisodesAtTheirLastActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	began := make(chan store.Event, 1)
	hub := motion.NewHub(st.Events(), motion.Options{Tick: time.Hour, Observe: func(e store.Event) {
		if e.End.IsZero() {
			began <- e
		}
	}})
	done := make(chan struct{})
	go func() { hub.Run(ctx); close(done) }()
	start := time.Now().UTC().Truncate(time.Millisecond)
	hub.CameraSource("cam_a", true)
	hub.CameraMotion("cam_a", true, start)
	<-began
	cancel()
	<-done
	rows, _ := st.Events().Range(context.Background(), "cam_a", start.Add(-time.Minute), start.Add(time.Minute))
	if len(rows) != 1 || !rows[0].End.Equal(start) {
		t.Fatalf("want the episode ended at its last activity %v, got %+v", start, rows)
	}
}

// A camera that has gone quiet (removed, or offline) with nothing open is
// forgotten; it comes back as new on its next frame or report.
func TestTheHubForgetsAQuietCamera(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := motion.Defaults()
	cfg.Hold = 20 * time.Millisecond
	hub := motion.NewHub(st.Events(), motion.Options{Config: cfg, Tick: 10 * time.Millisecond, Forget: 100 * time.Millisecond})
	go hub.Run(ctx)
	now := time.Now()
	hub.CameraSource("cam_a", true)
	hub.CameraMotion("cam_a", true, now)
	hub.CameraMotion("cam_a", false, now)
	hub.CameraSource("cam_a", false) // a report the loop sees only after the ones before
	deadline := time.Now().Add(2 * time.Second)
	for hub.Cameras() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the camera was never tracked")
		}
		time.Sleep(time.Millisecond)
	}
	for hub.Cameras() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("still tracking %d cameras", hub.Cameras())
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, _ := st.Events().Range(ctx, "cam_a", now.Add(-time.Minute), now.Add(time.Minute))
	if len(rows) != 1 || rows[0].End.IsZero() {
		t.Fatalf("the episode must have ended before the camera was forgotten: %+v", rows)
	}
}
