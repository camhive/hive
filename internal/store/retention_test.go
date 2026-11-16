package store_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// #61: a camera's own retention survives a restart, and the sweeper reads
// the cameras that have one (the rest follow the hive-wide value).
func TestCameraRetentionIsStoredAndListed(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	ninety, zero := 90, 0
	own := sample(t, "Own")
	own.Retention = camera.Retention{Days: &ninety}
	unlimited := sample(t, "Unlimited")
	unlimited.Retention = camera.Retention{Days: &zero}
	hive := sample(t, "Hive")
	for _, c := range []camera.Camera{own, unlimited, hive} {
		if err := s.Cameras().Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Cameras().Get(ctx, own.ID)
	if err != nil || got.Retention.Days == nil || *got.Retention.Days != 90 {
		t.Fatalf("own: %v, %v", got.Retention.Days, err)
	}
	if got, _ := s.Cameras().Get(ctx, hive.ID); got.Retention.Days != nil {
		t.Fatalf("hive default came back as %d", *got.Retention.Days)
	}
	days, err := s.Cameras().RetentionDays(ctx)
	want := map[string]int{string(own.ID): 90, string(unlimited.ID): 0}
	if err != nil || !maps.Equal(days, want) {
		t.Fatalf("RetentionDays = %v, %v; want %v", days, err, want)
	}

	back, _ := own.Update(camera.Patch{Retention: &camera.Retention{}}, time.Now())
	if err := s.Cameras().Put(ctx, back); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Cameras().Get(ctx, own.ID); got.Retention.Days != nil {
		t.Fatal("reset to the hive default was not stored")
	}
}
