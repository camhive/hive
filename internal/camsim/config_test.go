package camsim

import (
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		Host:      "127.0.0.1",
		RTSPPort:  18554,
		APIPort:   19997,
		ONVIFPort: 18080,
		Cameras: []Camera{
			{Name: "front", Codec: H264, Main: Resolution{1280, 720}, Sub: Resolution{640, 360}, FPS: 15},
			{Name: "yard", Codec: H265, Main: Resolution{1920, 1080}, Sub: Resolution{640, 360}, FPS: 10},
		},
	}
}

func TestStreamPathAndURL(t *testing.T) {
	cfg := testConfig()
	cam := cfg.Cameras[0]
	if got := cam.StreamPath(Main); got != "front/main" {
		t.Fatalf("StreamPath = %q", got)
	}
	if got := cfg.RTSPURL(cam, Sub); got != "rtsp://127.0.0.1:18554/front/sub" {
		t.Fatalf("RTSPURL = %q", got)
	}
}

func TestMediaMTXYAMLDeclaresEveryStreamPath(t *testing.T) {
	cfg := testConfig()
	y := cfg.mediamtxYAML()
	for _, want := range []string{"rtspAddress: :18554", "apiAddress: 127.0.0.1:19997", "front/main:", "front/sub:", "yard/main:", "yard/sub:"} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml missing %q\n%s", want, y)
		}
	}
	// Camera simulators publish; the server must not try to pull or record.
	for _, forbid := range []string{"source:", "record: true"} {
		if strings.Contains(y, forbid) {
			t.Errorf("yaml must not contain %q", forbid)
		}
	}
}

func TestFFmpegArgsEncodeWithRequestedCodecAndSize(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		cam    Camera
		stream Stream
		codec  string
		size   string
	}{
		{cfg.Cameras[0], Main, "libx264", "1280x720"},
		{cfg.Cameras[0], Sub, "libx264", "640x360"},
		{cfg.Cameras[1], Main, "libx265", "1920x1080"},
	}
	for _, tc := range cases {
		args := strings.Join(cfg.ffmpegArgs(tc.cam, tc.stream), " ")
		for _, want := range []string{"-c:v " + tc.codec, "size=" + tc.size, "rate=" + itoa(tc.cam.FPS), "-f rtsp", cfg.RTSPURL(tc.cam, tc.stream), "-re"} {
			if !strings.Contains(args, want) {
				t.Errorf("%s/%s: args missing %q\n%s", tc.cam.Name, tc.stream, want, args)
			}
		}
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := testConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	dup := testConfig()
	dup.Cameras[1].Name = "front"
	if err := dup.Validate(); err == nil {
		t.Error("duplicate camera names must be rejected")
	}
	badCodec := testConfig()
	badCodec.Cameras[0].Codec = "mpeg2"
	if err := badCodec.Validate(); err == nil {
		t.Error("unknown codec must be rejected")
	}
	none := testConfig()
	none.Cameras = nil
	if err := none.Validate(); err == nil {
		t.Error("config without cameras must be rejected")
	}
	badName := testConfig()
	badName.Cameras[0].Name = "has space"
	if err := badName.Validate(); err == nil {
		t.Error("camera name must be URL-path safe")
	}
}

func TestDefaultsFillZeroValues(t *testing.T) {
	cfg := Config{Cameras: []Camera{{Name: "a"}}}
	cfg.applyDefaults()
	if cfg.Host == "" || cfg.RTSPPort == 0 || cfg.APIPort == 0 || cfg.ONVIFPort == 0 {
		t.Fatalf("ports/host not defaulted: %+v", cfg)
	}
	c := cfg.Cameras[0]
	if c.Codec != H264 || c.FPS == 0 || c.Main.Width == 0 || c.Sub.Width == 0 {
		t.Fatalf("camera not defaulted: %+v", c)
	}
}
