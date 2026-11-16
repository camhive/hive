package events_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/events"
	"github.com/camhive/hive/internal/store"
)

var t0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func day(t *testing.T, st *store.Store, cam string) []store.Event {
	t.Helper()
	got, err := st.Events().Range(context.Background(), cam, t0.Add(-time.Hour), t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAConditionIsOneEventFromBeginToEnd(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	c := events.NewCondition(st.Events(), "cam_a", "motion")
	c.Begin(ctx, t0, map[string]any{"source": "frames"})
	c.Begin(ctx, t0.Add(time.Second), map[string]any{"source": "frames"}) // still the same episode
	got := day(t, st, "cam_a")
	if len(got) != 1 || !got[0].At.Equal(t0) || !got[0].End.IsZero() || got[0].Kind != "motion" {
		t.Fatalf("after begin: %+v", got)
	}
	var data map[string]string
	if err := json.Unmarshal(got[0].Data, &data); err != nil || data["source"] != "frames" {
		t.Fatalf("data %s", got[0].Data)
	}
	c.End(ctx, t0.Add(30*time.Second))
	c.End(ctx, t0.Add(time.Minute)) // nothing open any more
	got = day(t, st, "cam_a")
	if len(got) != 1 || !got[0].End.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("after end: %+v", got)
	}
	c.Begin(ctx, t0.Add(2*time.Minute), nil)
	if got = day(t, st, "cam_a"); len(got) != 2 || !got[1].End.IsZero() {
		t.Fatalf("a new episode is a new event: %+v", got)
	}
}

type recorder struct {
	began []int64
	ended []int64
}

func TestAConditionTellsItsObserverTheStableID(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	var r recorder
	c := events.NewCondition(st.Events(), "", "storage.low", events.Observe(func(e store.Event) {
		if e.End.IsZero() {
			r.began = append(r.began, e.ID)
		} else {
			r.ended = append(r.ended, e.ID)
		}
	}))
	c.Begin(ctx, t0, nil)
	c.End(ctx, t0.Add(time.Minute))
	rows := day(t, st, "cam_any")
	if len(rows) != 1 || len(r.began) != 1 || len(r.ended) != 1 || r.began[0] != rows[0].ID || r.ended[0] != rows[0].ID {
		t.Fatalf("observer saw began %v ended %v; rows %+v", r.began, r.ended, rows)
	}
}
