package camera_test

import (
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// #157: a camera starts at normal motion sensitivity; the owner sets low or
// high, and nothing else.
func TestSensitivityIsNormalUntilTheOwnerChoosesAnother(t *testing.T) {
	c, err := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/main"}}, time.Now())
	if err != nil || c.Sensitivity != camera.SensitivityNormal {
		t.Fatalf("new camera: %v, %v", c.Sensitivity, err)
	}
	low := camera.SensitivityLow
	u, err := c.Update(camera.Patch{Sensitivity: &low}, time.Now())
	if err != nil || u.Sensitivity != camera.SensitivityLow {
		t.Fatalf("low: %v, %v", u.Sensitivity, err)
	}
	if kept, _ := u.Update(camera.Patch{}, time.Now()); kept.Sensitivity != camera.SensitivityLow {
		t.Fatal("an unrelated patch reset the sensitivity")
	}
	bad := camera.Sensitivity(7)
	if _, err := c.Update(camera.Patch{Sensitivity: &bad}, time.Now()); !errors.Is(err, camera.ErrInvalid) {
		t.Fatalf("unknown sensitivity: %v", err)
	}
}

func TestParseSensitivityAcceptsTheThreeLevels(t *testing.T) {
	for _, s := range []string{"low", "normal", "high"} {
		if got, err := camera.ParseSensitivity(s); err != nil || got.String() != s {
			t.Fatalf("%s: %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "Normal", "max"} {
		if _, err := camera.ParseSensitivity(s); !errors.Is(err, camera.ErrInvalid) {
			t.Fatalf("%q: %v", s, err)
		}
	}
}
