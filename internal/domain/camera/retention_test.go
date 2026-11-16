package camera_test

import (
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

func days(n int) *camera.Retention { return &camera.Retention{Days: &n} }

// #61: a camera follows the hive-wide retention until the owner gives it its
// own number of days, and can be put back on the hive-wide value.
func TestRetentionFollowsTheHiveUntilSetAndCanBeReset(t *testing.T) {
	c, err := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/main"}}, time.Now())
	if err != nil || c.Retention.Days != nil {
		t.Fatalf("new camera: retention %v, %v", c.Retention.Days, err)
	}
	own, err := c.Update(camera.Patch{Retention: days(90)}, time.Now())
	if err != nil || own.Retention.Days == nil || *own.Retention.Days != 90 {
		t.Fatalf("own retention: %v, %v", own.Retention.Days, err)
	}
	if kept, _ := own.Update(camera.Patch{}, time.Now()); kept.Retention.Days == nil {
		t.Fatal("an unrelated patch dropped the camera's retention")
	}
	back, err := own.Update(camera.Patch{Retention: &camera.Retention{}}, time.Now())
	if err != nil || back.Retention.Days != nil {
		t.Fatalf("reset: %v, %v", back.Retention.Days, err)
	}
	if unlimited, err := c.Update(camera.Patch{Retention: days(0)}, time.Now()); err != nil || *unlimited.Retention.Days != 0 {
		t.Fatalf("0 days (no age limit): %v, %v", unlimited.Retention.Days, err)
	}
}

func TestRetentionDaysOutsideTheRangeAreRejected(t *testing.T) {
	c, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/main"}}, time.Now())
	for _, n := range []int{-1, camera.MaxRetentionDays + 1} {
		if _, err := c.Update(camera.Patch{Retention: days(n)}, time.Now()); !errors.Is(err, camera.ErrInvalid) {
			t.Fatalf("%d days: err = %v", n, err)
		}
	}
	if _, err := c.Update(camera.Patch{Retention: days(camera.MaxRetentionDays)}, time.Now()); err != nil {
		t.Fatalf("%d days: %v", camera.MaxRetentionDays, err)
	}
}
