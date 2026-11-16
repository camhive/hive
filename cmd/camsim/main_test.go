package main

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
)

func TestCameraFlagParsesCodecAndAudio(t *testing.T) {
	var c cameraFlags
	for _, v := range []string{"front", "yard:h265", "door:h264:audio", "gate:h264:motion", "porch:h265:audio:motion"} {
		if err := c.Set(v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	want := []camsim.Camera{{Name: "front"}, {Name: "yard", Codec: camsim.H265}, {Name: "door", Codec: camsim.H264, Audio: true},
		{Name: "gate", Codec: camsim.H264, Motion: 30 * time.Second}, {Name: "porch", Codec: camsim.H265, Audio: true, Motion: 30 * time.Second}}
	for i, w := range want {
		if c[i] != w {
			t.Errorf("camera %d = %+v, want %+v", i, c[i], w)
		}
	}
	for _, bad := range []string{"", ":h264", "door:h264:loud"} {
		if err := c.Set(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
