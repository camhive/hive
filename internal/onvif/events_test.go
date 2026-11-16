package onvif_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/onvif"
)

type reports struct {
	mu    sync.Mutex
	ready bool
	got   []bool
}

func (r *reports) onReady() { r.mu.Lock(); r.ready = true; r.mu.Unlock() }
func (r *reports) report(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, on)
}
func (r *reports) snapshot() (bool, []bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready, append([]bool(nil), r.got...)
}

// The camera's own motion detector (ADR-0020): a PullPoint subscription
// reports the initial state and then every change, on the mock's schedule
// (still for 2 s, moving for 1 s, every 3 s).
func TestWatchMotionReportsTheCamerasMotionStates(t *testing.T) {
	srv, _ := mockCamera(t, func(c *camsim.Config) {
		c.Username, c.Password = "admin", "pw"
		c.Cameras = []camsim.Camera{{Name: "gate", Codec: camsim.H264, Motion: 3 * time.Second}}
	})
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{Username: "admin", Password: "pw"})
	watch, cancel := context.WithCancel(context.Background())
	var r reports
	done := make(chan error, 1)
	go func() { done <- c.WatchMotion(watch, r.onReady, r.report) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, got := r.snapshot(); len(got) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("watch ended with %v", err)
	}
	ready, got := r.snapshot()
	if !ready || len(got) < 3 {
		t.Fatalf("ready %v, reports %v; want the initial state and changes", ready, got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("changes must alternate: %v", got)
		}
	}
}

func TestWatchMotionWithoutAnEventServiceIsErrNoEvents(t *testing.T) {
	srv, _ := mockCamera(t, func(c *camsim.Config) { c.NoEvents = true })
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	err := c.WatchMotion(ctx(t), func() { t.Error("ready without a subscription") }, func(bool) {})
	if !errors.Is(err, onvif.ErrNoEvents) {
		t.Fatalf("got %v, want ErrNoEvents", err)
	}
}

func TestWatchMotionEndsWhenTheSubscriptionIsGone(t *testing.T) {
	srv, _ := mockCamera(t, func(c *camsim.Config) {
		c.Cameras = []camsim.Camera{{Name: "gate", Codec: camsim.H264, Motion: 3 * time.Second}}
	})
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	done := make(chan error, 1)
	var r reports
	go func() { done <- c.WatchMotion(ctx(t), r.onReady, r.report) }()
	time.Sleep(300 * time.Millisecond)
	srv.CloseClientConnections()
	srv.Close() // the camera goes away
	select {
	case err := <-done:
		if err == nil || errors.Is(err, onvif.ErrNoEvents) {
			t.Fatalf("a lost camera is an error to retry, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("WatchMotion did not return after the camera went away")
	}
}

// Only a fault saying the camera cannot do it means "no events"; anything
// else (a clock-skewed or otherwise rejected request) is retried soon.
func TestOnlyAnUnsupportedFaultMeansNoEvents(t *testing.T) {
	for subcode, noEvents := range map[string]bool{"ActionNotSupported": true, "InvalidArgVal": false, "MessageExpired": false} {
		t.Run(subcode, func(t *testing.T) {
			h, _ := camsim.ONVIFHandler(camsim.Config{Cameras: []camsim.Camera{{Name: "gate", Codec: camsim.H264}}})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/onvif/events_service" {
					h.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/soap+xml")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><s:Fault><s:Code><s:Value>s:Sender</s:Value>`+
					`<s:Subcode><s:Value>ter:`+subcode+`</s:Value></s:Subcode></s:Code><s:Reason><s:Text>no</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`)
			}))
			defer srv.Close()
			c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
			err := c.WatchMotion(ctx(t), func() {}, func(bool) {})
			if err == nil || errors.Is(err, onvif.ErrNoEvents) != noEvents {
				t.Fatalf("fault %s: got %v, want ErrNoEvents = %v", subcode, err, noEvents)
			}
		})
	}
}
