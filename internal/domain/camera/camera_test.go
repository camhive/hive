package camera_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

func TestNewONVIFCameraIsValidAndHasID(t *testing.T) {
	c, err := camera.New(camera.Spec{
		Name:        "Front door",
		Source:      camera.Source{Kind: camera.SourceONVIF, Address: "http://192.168.1.10/onvif/device_service"},
		Credentials: camera.Credentials{Username: "admin", Password: "pw"},
	}, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(c.ID), "cam_") || len(c.ID) != 4+12 {
		t.Fatalf("id = %q", c.ID)
	}
	if !c.Enabled || c.CreatedAt.IsZero() || c.UpdatedAt != c.CreatedAt {
		t.Fatalf("camera = %+v", c)
	}
}

func TestNewRTSPCameraNeedsURLAndStripsEmbeddedCredentials(t *testing.T) {
	c, err := camera.New(camera.Spec{
		Name:   "Yard",
		Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://user:secret@10.0.0.5:554/Streaming/Channels/101"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Source.Address, "secret") {
		t.Fatalf("credentials must be moved out of the URL: %q", c.Source.Address)
	}
	if c.Credentials.Username != "user" || c.Credentials.Password != "secret" {
		t.Fatalf("credentials not extracted: %+v", c.Credentials)
	}
	if c.Streams[camera.Main].URL != "rtsp://10.0.0.5:554/Streaming/Channels/101" {
		t.Fatalf("main stream = %+v", c.Streams[camera.Main])
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]camera.Spec{
		"empty name":     {Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/x"}},
		"unknown kind":   {Name: "x", Source: camera.Source{Kind: "usb", Address: "x"}},
		"rtsp not rtsp":  {Name: "x", Source: camera.Source{Kind: camera.SourceRTSP, Address: "http://h/x"}},
		"onvif not http": {Name: "x", Source: camera.Source{Kind: camera.SourceONVIF, Address: "rtsp://h/x"}},
		"name too long":  {Name: strings.Repeat("a", 101), Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/x"}},
		"bad sub stream": {Name: "x", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/x"}, Streams: map[camera.Stream]camera.StreamSpec{camera.Sub: {URL: "ftp://h/y"}}},
		"unknown stream": {Name: "x", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/x"}, Streams: map[camera.Stream]camera.StreamSpec{"third": {URL: "rtsp://h/y"}}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := camera.New(spec, time.Now()); !errors.Is(err, camera.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUpdateChangesOnlyGivenFieldsAndBumpsUpdatedAt(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	c, _ := camera.New(camera.Spec{Name: "A", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, t0)
	name := "B"
	enabled := false
	group := "Outside"
	u, err := c.Update(camera.Patch{Name: &name, Enabled: &enabled, Group: &group}, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "B" || u.Enabled || u.Group != "Outside" || u.ID != c.ID || u.CreatedAt != t0 || u.UpdatedAt != t0.Add(time.Hour) {
		t.Fatalf("updated = %+v", u)
	}
	if c.Name != "A" || !c.Enabled {
		t.Fatal("Update must not mutate the receiver")
	}
	bad := ""
	if _, err := c.Update(camera.Patch{Name: &bad}, t0); !errors.Is(err, camera.ErrInvalid) {
		t.Fatalf("empty name via patch: %v", err)
	}
}

func TestSetStreamsFromONVIFProfiles(t *testing.T) {
	c, _ := camera.New(camera.Spec{Name: "A", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://h/onvif/device_service"}}, time.Now())
	u, err := c.WithStreams(map[camera.Stream]camera.StreamSpec{
		camera.Main: {URL: "rtsp://h/main", ProfileToken: "p1", Codec: "H265", Width: 1920, Height: 1080},
		camera.Sub:  {URL: "rtsp://h/sub", ProfileToken: "p2", Codec: "H264", Width: 640, Height: 360},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u.Streams[camera.Main].ProfileToken != "p1" || u.Streams[camera.Sub].Codec != "H264" {
		t.Fatalf("streams = %+v", u.Streams)
	}
	if _, err := c.WithStreams(map[camera.Stream]camera.StreamSpec{camera.Sub: {URL: "rtsp://h/sub"}}, time.Now()); !errors.Is(err, camera.ErrInvalid) {
		t.Fatal("main stream is mandatory")
	}
}

func TestRedactedStringNeverContainsSecrets(t *testing.T) {
	c, _ := camera.New(camera.Spec{Name: "A", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://u:topsecret@h/a"}}, time.Now())
	s := c.String()
	if strings.Contains(s, "topsecret") {
		t.Fatalf("String() leaked the password: %s", s)
	}
	if strings.Contains(c.Credentials.String(), "topsecret") {
		t.Fatal("Credentials.String() leaked the password")
	}
}
