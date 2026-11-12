package ptz_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/ptz"
)

func onvifCamera(t *testing.T, xaddr string, withProfile bool) camera.Camera {
	t.Helper()
	c, err := camera.New(camera.Spec{Name: "PTZ", Source: camera.Source{Kind: camera.SourceONVIF, Address: xaddr}, Credentials: camera.Credentials{Username: "admin", Password: "pw"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if withProfile {
		c, err = c.WithStreams(map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://h/m", ProfileToken: "prof_front_main"}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestControllerDrivesONVIFPTZ(t *testing.T) {
	cfg := camsim.Config{Username: "admin", Password: "pw", Cameras: []camsim.Camera{{Name: "front", Codec: camsim.H264}}}
	h, log := camsim.ONVIFHandler(cfg)
	srv := httptest.NewServer(h)
	defer srv.Close()
	cam := onvifCamera(t, srv.URL+"/onvif/device_service", true)
	ctl := ptz.New()
	ctx := context.Background()

	ok, err := ctl.Supported(ctx, cam)
	if err != nil || !ok {
		t.Fatalf("supported = %v err=%v", ok, err)
	}
	if err := ctl.Move(ctx, cam, 0.5, -0.5, 0); err != nil {
		t.Fatal(err)
	}
	if err := ctl.Stop(ctx, cam); err != nil {
		t.Fatal(err)
	}
	presets, err := ctl.Presets(ctx, cam)
	if err != nil || len(presets) != 3 {
		t.Fatalf("presets = %+v err=%v", presets, err)
	}
	if err := ctl.GotoPreset(ctx, cam, presets[1].Token); err != nil {
		t.Fatal(err)
	}
	got := log()
	if len(got) != 3 || got[0].Action != "ContinuousMove" || got[0].Pan != 0.5 || got[1].Action != "Stop" || got[2].Preset != "preset_2" {
		t.Fatalf("camera saw %+v", got)
	}
}

func TestControllerClampsVelocitiesAndRejectsOutOfRange(t *testing.T) {
	cfg := camsim.Config{Cameras: []camsim.Camera{{Name: "front", Codec: camsim.H264}}}
	h, log := camsim.ONVIFHandler(cfg)
	srv := httptest.NewServer(h)
	defer srv.Close()
	cam := onvifCamera(t, srv.URL+"/onvif/device_service", true)
	ctl := ptz.New()
	if err := ctl.Move(context.Background(), cam, 1.5, 0, 0); !errors.Is(err, ptz.ErrInvalid) {
		t.Fatalf("out of range: %v", err)
	}
	if len(log()) != 0 {
		t.Fatal("invalid move must not reach the camera")
	}
}

func TestRTSPCameraAndCameraWithoutProfileAreUnsupported(t *testing.T) {
	ctl := ptz.New()
	rtsp, _ := camera.New(camera.Spec{Name: "R", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/x"}}, time.Now())
	ok, err := ctl.Supported(context.Background(), rtsp)
	if err != nil || ok {
		t.Fatalf("rtsp supported = %v err=%v", ok, err)
	}
	if err := ctl.Move(context.Background(), rtsp, 0.1, 0, 0); !errors.Is(err, ptz.ErrUnsupported) {
		t.Fatalf("move on rtsp: %v", err)
	}
	cfg := camsim.Config{Cameras: []camsim.Camera{{Name: "front", Codec: camsim.H264}}}
	h, _ := camsim.ONVIFHandler(cfg)
	srv := httptest.NewServer(h)
	defer srv.Close()
	noProfile := onvifCamera(t, srv.URL+"/onvif/device_service", false)
	if err := ctl.Move(context.Background(), noProfile, 0.1, 0, 0); !errors.Is(err, ptz.ErrUnsupported) {
		t.Fatalf("move without profile: %v", err)
	}
}

func TestUnreachableCameraReportsErrorNotUnsupported(t *testing.T) {
	ctl := ptz.New()
	cam := onvifCamera(t, "http://127.0.0.1:1/onvif/device_service", true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := ctl.Supported(ctx, cam); err == nil || errors.Is(err, ptz.ErrUnsupported) {
		t.Fatalf("unreachable: %v", err)
	}
}
