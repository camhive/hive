package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func kinds(evs []store.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Camera+":"+e.Kind)
	}
	return out
}

func TestRangeIncludesHiveLevelEventsWithTheCamerasOwn(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	for _, e := range []store.Event{
		{Camera: "cam_a", Kind: "motion", At: t0},
		{Camera: "", Kind: "storage.low", At: t0.Add(time.Minute)},
		{Camera: "cam_b", Kind: "motion", At: t0.Add(2 * time.Minute)},
	} {
		if _, err := ev.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cam_a:motion", ":storage.low"}; len(got) != 2 || kinds(got)[0] != want[0] || kinds(got)[1] != want[1] {
		t.Fatalf("got %v, want %v", kinds(got), want)
	}
}

func TestAnOpenEventIsClosedByItsIDAndKeepsTheEnd(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	id, err := ev.Begin(ctx, store.Event{Camera: "cam_a", Kind: "motion", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if len(got) != 1 || !got[0].End.IsZero() {
		t.Fatalf("open event: %+v", got)
	}
	end := t0.Add(42 * time.Second)
	if err := ev.End(ctx, id, end); err != nil {
		t.Fatal(err)
	}
	got, _ = ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if len(got) != 1 || got[0].ID != id || !got[0].End.Equal(end) {
		t.Fatalf("closed event: %+v", got)
	}
}

func TestRangeReturnsEventsThatOverlapItsStart(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "motion", At: t0.Add(-time.Minute), End: t0.Add(time.Minute)})
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "motion", At: t0.Add(-3 * time.Minute), End: t0.Add(-2 * time.Minute)})
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "camera.offline", At: t0.Add(-time.Second)})
	got, err := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].At.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("want only the motion spanning from, got %+v", got)
	}
}

func TestCloseOpenEndsEventsLeftOpenByACrash(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	_, _ = ev.Begin(ctx, store.Event{Camera: "cam_a", Kind: "motion", At: t0})
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "motion", At: t0.Add(time.Second), End: t0.Add(5 * time.Second)})
	n, err := ev.CloseOpen(ctx)
	if err != nil || n != 1 {
		t.Fatalf("closed %d, err %v", n, err)
	}
	if n, _ := ev.CloseOpen(ctx); n != 0 {
		t.Fatalf("second run closed %d, want 0", n)
	}
	got, _ := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if !got[0].End.Equal(t0) || !got[1].End.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("the open one must end at its start, the ended one keep its end: %+v", got)
	}
}

// An Event that lasts and is still open (storage.low since yesterday, motion
// across midnight) belongs to every range after its start; an instantaneous
// one ends where it began, so only its own moment counts.
func TestAnOpenEventThatBeganBeforeTheRangeIsInIt(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	if _, err := ev.Begin(ctx, store.Event{Kind: "storage.low", At: t0.Add(-24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "camera.offline", At: t0.Add(-time.Hour)})
	got, err := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "storage.low" || !got[0].End.IsZero() {
		t.Fatalf("want only the open storage.low, got %+v", got)
	}
}

func TestAnInstantaneousEventEndsWhereItBegan(t *testing.T) {
	ctx := context.Background()
	ev := open(t).Events()
	_, _ = ev.Append(ctx, store.Event{Camera: "cam_a", Kind: "camera.offline", At: t0})
	got, _ := ev.Range(ctx, "cam_a", t0, t0.Add(time.Hour))
	if len(got) != 1 || !got[0].End.Equal(t0) {
		t.Fatalf("got %+v", got)
	}
}
