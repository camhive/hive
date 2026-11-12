package camera_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// ADR-0015: sound is recorded only when the owner switches it on.
func TestRecordAudioIsOffUntilSwitchedOn(t *testing.T) {
	c, err := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/main"}}, time.Now())
	if err != nil || c.RecordAudio {
		t.Fatalf("new camera: record audio %v, %v", c.RecordAudio, err)
	}
	on := true
	u, err := c.Update(camera.Patch{RecordAudio: &on}, time.Now())
	if err != nil || !u.RecordAudio || c.RecordAudio {
		t.Fatalf("switched on: %v (original %v), %v", u.RecordAudio, c.RecordAudio, err)
	}
	if v, _ := u.Update(camera.Patch{}, time.Now()); !v.RecordAudio {
		t.Fatal("an unrelated patch switched sound off")
	}
}
