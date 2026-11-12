package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// ADR-0016: a deletion that waits for a segment still being written
// survives a restart until it is done.
func TestPendingDeletionsArePersistedUntilDone(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	r := s.Segments()
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a := store.PendingDeletion{Camera: "cam_1", From: t0, To: t0.Add(time.Hour), CreatedAt: t0.Add(2 * time.Hour)}
	b := store.PendingDeletion{Camera: "cam_2", To: t0} // from the beginning
	for _, p := range []store.PendingDeletion{a, b} {
		if err := r.AddPending(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID == 0 || got[0].Camera != "cam_1" || !got[0].From.Equal(t0) || !got[0].To.Equal(t0.Add(time.Hour)) || !got[1].From.IsZero() {
		t.Fatalf("pending = %+v", got)
	}
	if err := r.DonePending(ctx, got[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.Pending(ctx); len(got) != 1 || got[0].Camera != "cam_2" {
		t.Fatalf("after done = %+v", got)
	}
}
