package store_test

import (
	"context"
	"testing"

	"github.com/camhive/hive/internal/domain/camera"
)

// #157: a camera's motion sensitivity survives a restart.
func TestCameraSensitivityIsStored(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	low, plain := sample(t, "Low"), sample(t, "Plain")
	low.Sensitivity = camera.SensitivityLow
	for _, c := range []camera.Camera{low, plain} {
		if err := s.Cameras().Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Cameras().Get(ctx, low.ID); err != nil || got.Sensitivity != camera.SensitivityLow {
		t.Fatalf("low: %v, %v", got.Sensitivity, err)
	}
	if got, _ := s.Cameras().Get(ctx, plain.ID); got.Sensitivity != camera.SensitivityNormal {
		t.Fatalf("plain: %v", got.Sensitivity)
	}
}
