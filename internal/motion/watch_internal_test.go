package motion

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/onvif"
)

func TestRetriesBackOffToTheCapAndWaitLongWithoutEvents(t *testing.T) {
	w := NewWatcher(nil, nil, WatchOptions{})
	broken := errors.New("connection refused")
	got := make([]time.Duration, 0, 8)
	wait := time.Duration(0)
	for range 8 {
		wait = w.retryAfter(broken, wait)
		got = append(got, wait)
	}
	want := "[5s 10s 20s 40s 1m20s 2m40s 5m0s 5m0s]"
	if fmt.Sprint(got) != want {
		t.Fatalf("backoff %v, want %s", got, want)
	}
	if d := w.retryAfter(fmt.Errorf("x: %w", onvif.ErrNoEvents), time.Second); d != time.Hour {
		t.Fatalf("a camera without events is asked again after %v, want 1h", d)
	}
}

func TestAWatchIsStaleWhenTheCameraGoesOrItsAccessChanges(t *testing.T) {
	c := camera.Camera{ID: "cam_a", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://h/onvif/device_service"}, Credentials: camera.Credentials{Username: "u", Password: "p"}}
	key := keyOf(c)
	if stale(key, c.ID, map[camera.ID]camera.Camera{c.ID: c}) {
		t.Fatal("an unchanged camera keeps its watch")
	}
	if !stale(key, c.ID, map[camera.ID]camera.Camera{}) {
		t.Fatal("a camera no longer watched must stop")
	}
	moved := c
	moved.Credentials.Password = "new"
	if !stale(key, c.ID, map[camera.ID]camera.Camera{c.ID: moved}) {
		t.Fatal("new credentials must restart the watch")
	}
}
