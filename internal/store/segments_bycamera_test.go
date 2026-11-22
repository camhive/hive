package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// #150: the storage overview's sums per camera, both streams together; the
// newest end comes from the newest segment's duration, to the millisecond;
// a camera without footage in a window has no first segment there.
func TestSegmentsByCamera(t *testing.T) {
	ctx := context.Background()
	r := open(t).Segments()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	rows := []store.SegmentRow{
		{Camera: "cam_b", Stream: "main", Start: now.Add(-9 * 24 * time.Hour), Duration: 10 * time.Minute, Bytes: 7, Path: "/b1"},
		{Camera: "cam_a", Stream: "main", Start: now.Add(-3 * 24 * time.Hour), Duration: 10 * time.Minute, Bytes: 100, Path: "/a1", Complete: true},
		{Camera: "cam_a", Stream: "main", Start: now.Add(-2 * time.Hour), Duration: 10 * time.Minute, Bytes: 50, Path: "/a2", Complete: true},
		{Camera: "cam_a", Stream: "sub", Start: now.Add(-time.Hour), Duration: 1234567 * time.Microsecond, Bytes: 5, Path: "/a3"},
	}
	for _, row := range rows {
		if err := r.Upsert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.ByCamera(ctx, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []store.CameraBytes{
		{Camera: "cam_a", Bytes: 155, Oldest: now.Add(-3 * 24 * time.Hour), Newest: now.Add(-time.Hour + 1234*time.Millisecond), Since24h: 55, Since7d: 155,
			First24h: now.Add(-2 * time.Hour), First7d: now.Add(-3 * 24 * time.Hour)},
		{Camera: "cam_b", Bytes: 7, Oldest: now.Add(-9 * 24 * time.Hour), Newest: now.Add(-9*24*time.Hour + 10*time.Minute)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Camera != w.Camera || g.Bytes != w.Bytes || !g.Oldest.Equal(w.Oldest) || !g.Newest.Equal(w.Newest) || g.Since24h != w.Since24h || g.Since7d != w.Since7d ||
			!g.First24h.Equal(w.First24h) || !g.First7d.Equal(w.First7d) {
			t.Errorf("%d: got %+v, want %+v", i, g, w)
		}
	}
	if empty, err := open(t).Segments().ByCamera(ctx, now, now); err != nil || len(empty) != 0 {
		t.Fatalf("empty index: %v %v", empty, err)
	}
}
