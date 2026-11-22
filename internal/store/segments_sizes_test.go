package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// Camera care (#163) reads only start, length and size of one stream's
// segments that start in a window, oldest first.
func TestSegmentSizesOfAStreamInAWindow(t *testing.T) {
	ctx := context.Background()
	r := open(t).Segments()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, row := range []store.SegmentRow{
		{Camera: "cam_a", Stream: "main", Start: now.Add(-2 * time.Hour), Duration: 10 * time.Minute, Bytes: 1, Path: "/1"},
		{Camera: "cam_a", Stream: "main", Start: now.Add(-20 * time.Minute), Duration: 10 * time.Minute, Bytes: 2, Path: "/2"},
		{Camera: "cam_a", Stream: "main", Start: now.Add(-50 * time.Minute), Duration: 1500 * time.Millisecond, Bytes: 3, Path: "/3"},
		{Camera: "cam_a", Stream: "sub", Start: now.Add(-10 * time.Minute), Duration: time.Minute, Bytes: 4, Path: "/4"},
		{Camera: "cam_b", Stream: "main", Start: now.Add(-10 * time.Minute), Duration: time.Minute, Bytes: 5, Path: "/5"},
	} {
		if err := r.Upsert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Sizes(ctx, "cam_a", "main", now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.SegmentSize{{Start: now.Add(-50 * time.Minute), Duration: 1500 * time.Millisecond, Bytes: 3}, {Start: now.Add(-20 * time.Minute), Duration: 10 * time.Minute, Bytes: 2}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("sizes = %+v", got)
	}
}
