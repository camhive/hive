package onvif_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/onvif"
)

func mockCamera(t *testing.T, mutate func(*camsim.Config)) (*httptest.Server, func() []camsim.PTZCommand) {
	t.Helper()
	cfg := camsim.Config{
		Cameras: []camsim.Camera{
			{Name: "front", Codec: camsim.H264, Main: camsim.Resolution{Width: 1280, Height: 720}, Sub: camsim.Resolution{Width: 640, Height: 360}, FPS: 15},
			{Name: "yard", Codec: camsim.H265, Main: camsim.Resolution{Width: 1920, Height: 1080}, Sub: camsim.Resolution{Width: 640, Height: 360}, FPS: 10},
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h, ptz := camsim.ONVIFHandler(cfg)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, ptz
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestDeviceInformation(t *testing.T) {
	srv, _ := mockCamera(t, nil)
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	info, err := c.DeviceInformation(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.Manufacturer != "Camhive" || info.Model != "camsim" || info.SerialNumber == "" {
		t.Fatalf("info = %+v", info)
	}
}

func TestProfilesAndStreamURI(t *testing.T) {
	srv, _ := mockCamera(t, nil)
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	profiles, err := c.Profiles(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 4 {
		t.Fatalf("got %d profiles, want 4: %+v", len(profiles), profiles)
	}
	var yardMain *onvif.Profile
	for i := range profiles {
		if profiles[i].Token == "prof_yard_main" {
			yardMain = &profiles[i]
		}
	}
	if yardMain == nil || yardMain.Encoding != "H265" || yardMain.Width != 1920 || yardMain.Height != 1080 || yardMain.FPS != 10 {
		t.Fatalf("yard/main = %+v", yardMain)
	}
	uri, err := c.StreamURI(ctx(t), "prof_yard_main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "rtsp://") || !strings.HasSuffix(uri, "/yard/main") {
		t.Fatalf("uri = %q", uri)
	}
}

func TestQuirkyCameraStillResolvesMediaServiceAndKeepsQueryInURI(t *testing.T) {
	srv, _ := mockCamera(t, func(c *camsim.Config) { c.Profile = camsim.Quirky })
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	// The mock advertises its configured host, not the httptest address; the
	// client must rebase service XAddrs onto the device address it was given.
	profiles, err := c.Profiles(ctx(t))
	if err != nil {
		t.Fatalf("Profiles on quirky camera: %v", err)
	}
	if len(profiles) != 4 {
		t.Fatalf("got %d profiles", len(profiles))
	}
	uri, err := c.StreamURI(ctx(t), "prof_front_sub")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "/front/sub?profile=prof_front_sub") {
		t.Fatalf("query string must be preserved: %q", uri)
	}
}

func TestAuthenticationWithUsernameToken(t *testing.T) {
	srv, _ := mockCamera(t, func(c *camsim.Config) { c.Username, c.Password = "admin", "s3cret" })

	_, err := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{}).Profiles(ctx(t))
	if !errors.Is(err, onvif.ErrUnauthorized) {
		t.Fatalf("without credentials: %v, want ErrUnauthorized", err)
	}
	_, err = onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{Username: "admin", Password: "nope"}).Profiles(ctx(t))
	if !errors.Is(err, onvif.ErrUnauthorized) {
		t.Fatalf("wrong password: %v, want ErrUnauthorized", err)
	}
	profiles, err := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{Username: "admin", Password: "s3cret"}).Profiles(ctx(t))
	if err != nil || len(profiles) != 4 {
		t.Fatalf("valid credentials: %v (%d profiles)", err, len(profiles))
	}
}

func TestUnknownProfileIsTypedFault(t *testing.T) {
	srv, _ := mockCamera(t, nil)
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	_, err := c.StreamURI(ctx(t), "nope")
	var f *onvif.Fault
	if !errors.As(err, &f) || f.Subcode != "NoProfile" {
		t.Fatalf("err = %v, want Fault NoProfile", err)
	}
}

func TestPTZCommandsReachTheCamera(t *testing.T) {
	srv, ptzLog := mockCamera(t, nil)
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	caps, err := c.Capabilities(ctx(t))
	if err != nil || !caps.PTZ {
		t.Fatalf("capabilities = %+v err=%v", caps, err)
	}
	if err := c.ContinuousMove(ctx(t), "prof_front_main", 0.5, -0.25, 0.1); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx(t), "prof_front_main"); err != nil {
		t.Fatal(err)
	}
	presets, err := c.Presets(ctx(t), "prof_front_main")
	if err != nil || len(presets) != 3 || presets[1].Token != "preset_2" || presets[1].Name != "Preset 2" {
		t.Fatalf("presets = %+v err=%v", presets, err)
	}
	if err := c.GotoPreset(ctx(t), "prof_front_main", "preset_2"); err != nil {
		t.Fatal(err)
	}
	got := ptzLog()
	if len(got) != 3 || got[0].Action != "ContinuousMove" || got[0].Pan != 0.5 || got[0].Tilt != -0.25 || got[0].Zoom != 0.1 ||
		got[1].Action != "Stop" || got[2].Action != "GotoPreset" || got[2].Preset != "preset_2" {
		t.Fatalf("camera saw %+v", got)
	}
}

func TestSetSystemDateAndTime(t *testing.T) {
	srv, _ := mockCamera(t, nil)
	c := onvif.NewClient(srv.URL+"/onvif/device_service", onvif.Credentials{})
	if err := c.SetSystemDateAndTime(ctx(t), time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFindsTheMockByUnicastProbe(t *testing.T) {
	cfg := camsim.Config{Cameras: []camsim.Camera{{Name: "one"}}, Host: "127.0.0.1", ONVIFPort: 18089}
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := camsim.ServeDiscovery(c, cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	found, err := onvif.Discover(ctx(t), onvif.DiscoverOptions{Timeout: time.Second, Address: addr.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.Contains(found[0].XAddr, "127.0.0.1:18089/onvif/device_service") || found[0].Name != "camsim" {
		t.Fatalf("found = %+v", found)
	}
}

func TestUnreachableCameraReturnsError(t *testing.T) {
	c := onvif.NewClient("http://127.0.0.1:1/onvif/device_service", onvif.Credentials{})
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.DeviceInformation(cctx); err == nil {
		t.Fatal("expected a transport error")
	}
}
