package camera_test

import (
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func onvifCam(t *testing.T) camera.Camera {
	t.Helper()
	c, err := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://192.168.1.10:8000/onvif/device_service"},
		Streams: map[camera.Stream]camera.StreamSpec{
			camera.Main: {URL: "rtsp://192.168.1.10:554/main", ProfileToken: "p1"},
			camera.Sub:  {URL: "rtsp://192.168.1.10/sub"},
		}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ADR-0017: identity fills empty fields only; a known field that differs is
// reported, not overwritten.
func TestLearnFillsEmptyIdentityFieldsAndReportsConflicts(t *testing.T) {
	c := onvifCam(t)
	got, conflicts := c.Learn(camera.Identity{MAC: "AA:BB:CC:00:11:22", Serial: "SN1"}, t0)
	if conflicts != nil || got.Identity.MAC != "aa:bb:cc:00:11:22" || got.Identity.Serial != "SN1" {
		t.Fatalf("identity = %+v conflicts %v", got.Identity, conflicts)
	}
	again, conflicts := got.Learn(camera.Identity{MAC: "aa:bb:cc:00:11:99", Serial: "SN1", Model: "X1"}, t0)
	if again.Identity.MAC != "aa:bb:cc:00:11:22" || again.Identity.Model != "X1" || len(conflicts) != 1 || conflicts[0] != "mac" {
		t.Fatalf("identity = %+v conflicts %v", again.Identity, conflicts)
	}
}

// A found device matches only when every field both sides know agrees, and
// they share at least one.
func TestIdentityMatchNeedsEveryKnownFieldAndOneInCommon(t *testing.T) {
	known := camera.Identity{MAC: "aa:bb:cc:00:11:22", ONVIFEndpoint: "urn:uuid:1", Serial: "SN1"}
	for _, tc := range []struct {
		found camera.Identity
		want  bool
	}{
		{camera.Identity{MAC: "AA:BB:CC:00:11:22", ONVIFEndpoint: "urn:uuid:1", Serial: "SN1"}, true},
		{camera.Identity{MAC: "aa:bb:cc:00:11:22"}, true},
		{camera.Identity{MAC: "aa:bb:cc:00:11:22", Serial: "SN2"}, false}, // right MAC, wrong serial: spoofed
		{camera.Identity{Model: "X1"}, false},                             // nothing in common
		{camera.Identity{}, false},
	} {
		if got := known.Matches(tc.found); got != tc.want {
			t.Errorf("%+v: %v, want %v", tc.found, got, tc.want)
		}
	}
	if (camera.Identity{}).Matches(camera.Identity{MAC: "aa:bb:cc:00:11:22"}) {
		t.Error("an unknown camera must match nothing")
	}
}

// Moving swaps the host in the source and in streams on the same host,
// keeping scheme, port and path; the id and everything else stay.
func TestMoveToSwapsTheHostKeepingPortsAndPaths(t *testing.T) {
	c := onvifCam(t)
	c.Streams[camera.Sub] = camera.StreamSpec{URL: "rtsp://192.168.1.10/sub"}
	moved, err := c.MoveTo("192.168.1.23", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != c.ID || moved.Source.Address != "http://192.168.1.23:8000/onvif/device_service" ||
		moved.Streams[camera.Main].URL != "rtsp://192.168.1.23:554/main" || moved.Streams[camera.Main].ProfileToken != "p1" ||
		moved.Streams[camera.Sub].URL != "rtsp://192.168.1.23/sub" || !moved.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("moved = %+v", moved)
	}
	if c.Host() != "192.168.1.10" || moved.Host() != "192.168.1.23" {
		t.Fatalf("hosts %q %q", c.Host(), moved.Host())
	}
	for _, bad := range []string{"", "192.168.1.23:80", "a b", "http://x"} {
		if _, err := c.MoveTo(bad, t0); !errors.Is(err, camera.ErrInvalid) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	// A stream on another host (a separate encoder) is left alone.
	c.Streams[camera.Sub] = camera.StreamSpec{URL: "rtsp://10.0.0.5/sub"}
	if moved, _ := c.MoveTo("192.168.1.23", t0); moved.Streams[camera.Sub].URL != "rtsp://10.0.0.5/sub" {
		t.Fatalf("other host moved: %v", moved.Streams[camera.Sub].URL)
	}
}

func TestRTSPCameraMovesItsMainURL(t *testing.T) {
	c, _ := camera.New(camera.Spec{Name: "Yard", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://192.168.1.11:554/h264"}}, t0)
	moved, err := c.MoveTo("192.168.1.40", t0)
	if err != nil || moved.Source.Address != "rtsp://192.168.1.40:554/h264" || moved.Streams[camera.Main].URL != "rtsp://192.168.1.40:554/h264" {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
}
