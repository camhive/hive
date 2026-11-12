package export_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/store"
)

// ADR-0016: deleting footage can take the exports made from it along.
func TestRemoveDeletesMatchingExportsAndRefusesWhileOneIsRunning(t *testing.T) {
	dir := t.TempDir()
	repo := jobs(t)
	m := export.NewManager(dir, fetchOK, repo)
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	put := func(id, camera, status string, start time.Time) {
		t.Helper()
		if err := repo.Put(ctx, store.ExportJob{ID: id, Owner: "acc_1", Camera: camera, Stream: "main", Start: start, Duration: 10 * time.Minute, Status: status, Bytes: 10, FileName: id + ".mp4", CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".mp4"), []byte("0123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("exp_hit", "cam_1", "done", t0)
	put("exp_other_cam", "cam_2", "done", t0)
	put("exp_later", "cam_1", "failed", t0.Add(3*time.Hour))
	// cam_1 from 09:55 to 10:05 overlaps exp_hit (10:00–10:10) only.
	match := func(camera string, from, to time.Time) bool {
		return camera == "cam_1" && from.Before(t0.Add(5*time.Minute)) && to.After(t0.Add(-5*time.Minute))
	}

	plan, err := m.Remove(ctx, match, true)
	if err != nil || plan.Jobs != 1 || plan.Bytes != 10 {
		t.Fatalf("dry run = %+v, %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "exp_hit.mp4")); err != nil {
		t.Fatal("dry run removed the file")
	}
	done, err := m.Remove(ctx, match, false)
	if err != nil || done != plan {
		t.Fatalf("remove = %+v, %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "exp_hit.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file left behind")
	}
	if _, err := m.Get(ctx, "exp_hit"); err == nil {
		t.Fatal("job left behind")
	}
	for _, id := range []string{"exp_other_cam", "exp_later"} {
		if _, err := m.Get(ctx, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	// A running export of matching footage would bring it back: refuse.
	put("exp_running", "cam_1", "running", t0)
	if _, err := m.Remove(ctx, match, true); !errors.Is(err, export.ErrRunning) {
		t.Fatalf("err = %v, want ErrRunning", err)
	}
}
