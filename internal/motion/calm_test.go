package motion_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/store"
)

// calmHub runs a Hub on a fresh store with fast timings and a calm rule of
// more than 3 episodes an hour, stepping back after `quiet` without one.
type calmHub struct {
	hub    *motion.Hub
	events chan store.Event
	calms  chan motion.Calming
}

func newCalmHub(t *testing.T, quiet time.Duration) calmHub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg := motion.Defaults()
	cfg.Hold = time.Millisecond
	h := calmHub{events: make(chan store.Event, 64), calms: make(chan motion.Calming, 8)}
	h.hub = motion.NewHub(st.Events(), motion.Options{Config: cfg, Tick: 5 * time.Millisecond,
		Calm:    motion.CalmRule{Limit: 3, Window: time.Hour, Quiet: quiet},
		Observe: func(e store.Event) { h.events <- e }, OnCalm: func(c motion.Calming) { h.calms <- c }})
	done := make(chan struct{})
	go func() { h.hub.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; _ = st.Close() })
	h.hub.CameraSource("cam_a", true)
	return h
}

// blip is a short report of the camera's own detector.
func (h calmHub) blip(at time.Time) {
	h.hub.CameraMotion("cam_a", true, at)
	h.hub.CameraMotion("cam_a", false, at)
}

// episode is a blip that became an episode, begun and ended.
func (h calmHub) episode(t *testing.T, at time.Time) {
	t.Helper()
	h.blip(at)
	for _, want := range []bool{true, false} {
		select {
		case e := <-h.events:
			if e.End.IsZero() != want {
				t.Fatalf("event %+v", e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no episode")
		}
	}
}

// noEpisode checks that a blip begins nothing.
func (h calmHub) noEpisode(t *testing.T, at time.Time) {
	t.Helper()
	h.blip(at)
	select {
	case e := <-h.events:
		t.Fatalf("a short report began an episode: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// #157: a camera raising more episodes in an hour than the rule allows is
// calmed one step, says so once with the count, and steps back after a
// quiet spell. Every episode still becomes an Event.
func TestABusyCameraIsCalmedAndStepsBack(t *testing.T) {
	h := newCalmHub(t, 300*time.Millisecond)
	now := time.Now().UTC()
	for i := range 4 {
		h.episode(t, now.Add(time.Duration(i)*time.Millisecond))
	}
	var c motion.Calming
	select {
	case c = <-h.calms:
	case <-time.After(2 * time.Second):
		t.Fatal("not calmed")
	}
	if !c.Calmed || c.Camera != "cam_a" || c.Episodes != 4 || !c.At.Equal(now.Add(3*time.Millisecond)) || c.Sensitivity != camera.SensitivityNormal {
		t.Fatalf("calming %+v", c)
	}
	if since, ok := h.hub.CalmedSince("cam_a"); !ok || !since.Equal(c.At) {
		t.Fatalf("CalmedSince = %v, %v", since, ok)
	}
	h.noEpisode(t, time.Now()) // one step less sensitive: low wants a lasting report

	select {
	case c = <-h.calms:
	case <-time.After(2 * time.Second):
		t.Fatal("never stepped back")
	}
	if c.Calmed || c.Camera != "cam_a" {
		t.Fatalf("step back %+v", c)
	}
	if _, ok := h.hub.CalmedSince("cam_a"); ok {
		t.Fatal("still calmed")
	}
	h.episode(t, time.Now())
}

// The owner's sensitivity reaches the camera's tracker.
func TestTheHubAppliesTheCamerasSensitivity(t *testing.T) {
	h := newCalmHub(t, time.Hour)
	h.hub.Sensitivity("cam_a", camera.SensitivityLow)
	h.noEpisode(t, time.Now())
	h.hub.Sensitivity("cam_a", camera.SensitivityNormal)
	h.episode(t, time.Now())
}

// #157 review: a camera removed from the hive leaves nothing behind: its
// calmed note and its sensitivity go with it.
func TestTheHubForgetsRemovedCameras(t *testing.T) {
	h := newCalmHub(t, time.Hour)
	h.hub.Sensitivity("cam_a", camera.SensitivityHigh)
	now := time.Now().UTC()
	for i := range 4 {
		h.episode(t, now.Add(time.Duration(i)*time.Millisecond))
	}
	<-h.calms
	h.hub.Known([]string{"cam_b"}) // cam_a was removed
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := h.hub.CalmedSince("cam_a"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a removed camera is still calmed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Back again, it starts at normal and uncalmed: a blip is an episode.
	h.episode(t, time.Now())
}
