package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

func exportRow(id, owner string, created time.Time) store.ExportJob {
	return store.ExportJob{ID: id, Owner: owner, Camera: "cam_1", Stream: "main", Start: created.Add(-time.Hour), Duration: 90 * time.Second,
		Status: "running", FileName: id + ".mp4", CreatedAt: created}
}

// #59: export jobs survive a restart of the hive.
func TestExportJobsArePersistedAndListedPerOwnerNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	r := s.Exports()
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	a, b, c := exportRow("exp_a", "acc_1", t0), exportRow("exp_b", "acc_1", t0.Add(time.Minute)), exportRow("exp_c", "acc_2", t0)
	for _, j := range []store.ExportJob{a, b, c} {
		if err := r.Put(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	// Finishing a job is a Put of the same id.
	a.Status, a.Bytes, a.DoneAt = "done", 1234, t0.Add(2*time.Minute)
	if err := r.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "exp_a")
	if err != nil || got.Status != "done" || got.Bytes != 1234 || !got.DoneAt.Equal(a.DoneAt) || !got.Start.Equal(a.Start) || got.Duration != a.Duration || got.FileName != "exp_a.mp4" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	mine, err := r.List(ctx, "acc_1")
	if err != nil || len(mine) != 2 || mine[0].ID != "exp_b" || mine[1].ID != "exp_a" {
		t.Fatalf("list = %+v, %v", mine, err)
	}
	all, _ := r.All(ctx)
	if len(all) != 3 {
		t.Fatalf("all = %+v", all)
	}
	if err := r.Delete(ctx, "exp_a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "exp_a"); err != nil {
		t.Fatalf("deleting twice is fine: %v", err)
	}
	if _, err := r.Get(ctx, "exp_a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted job = %v", err)
	}
	if b, _ := r.Get(ctx, "exp_b"); !b.DoneAt.IsZero() {
		t.Fatalf("a running job has no done time: %+v", b)
	}
}

func TestExportJobRejectsAnUnknownStatus(t *testing.T) {
	r := open(t).Exports()
	j := exportRow("exp_x", "acc_1", time.Now())
	j.Status = "paused"
	if err := r.Put(context.Background(), j); err == nil {
		t.Fatal("unknown status stored")
	}
}
