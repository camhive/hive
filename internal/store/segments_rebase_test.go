package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// #155: hive move-data points the rows at the archive on the new disk;
// a row outside the old archive, or one whose directory only shares the
// prefix, keeps its path.
func TestRebaseMovesThePathsOfTheOldArchiveOnly(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	r := st.Segments()
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for i, p := range []string{"/old/archivé/cam_a/main/1.mp4", "/old/archivé/cam_a/main/2.mp4.age", "/old/archivé2/cam_b/main/3.mp4", "/elsewhere/4.mp4"} {
		if err := r.Upsert(ctx, store.SegmentRow{Camera: "cam", Stream: "main", Start: at.Add(time.Duration(i) * time.Minute), Path: p}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.Rebase(ctx, "/old/archivé", "/new/camhive/archive")
	if err != nil || n != 2 {
		t.Fatalf("rebased %d, %v; want 2", n, err)
	}
	rows, err := r.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/new/camhive/archive/cam_a/main/1.mp4", "/new/camhive/archive/cam_a/main/2.mp4.age", "/old/archivé2/cam_b/main/3.mp4", "/elsewhere/4.mp4"}
	for i, row := range rows {
		if row.Path != want[i] {
			t.Errorf("row %d path = %s, want %s", i, row.Path, want[i])
		}
	}
	if err := st.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}

// #155 review: a checkpoint that a reader holds back is retried and then
// reported, not taken for done.
func TestACheckpointHeldBackByAReaderIsReported(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hive.db")
	key := make([]byte, 32)
	st, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reader, err := store.Open(ctx, path, key) // the running hive, another process
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := st.Actions().Append(ctx, store.Action{Actor: "owner", Action: "a", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	release, err := reader.HoldReadForTest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Actions().Append(ctx, store.Action{Actor: "owner", Action: "b", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Checkpoint(ctx); !errors.Is(err, store.ErrCheckpointBusy) {
		t.Fatalf("checkpoint with a reader = %v, want ErrCheckpointBusy", err)
	}
	release()
	if err := st.Checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint after the reader = %v", err)
	}
}
