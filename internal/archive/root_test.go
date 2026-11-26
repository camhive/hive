package archive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
)

// #172: a missing archive root (the disk's mount vanished) fails the
// reconcile and keeps every row; it never reads as an empty archive.
func TestAMissingRootKeepsTheIndex(t *testing.T) {
	ctx := context.Background()
	idx := archive.NewIndex(openStore(t))
	root := filepath.Join(t.TempDir(), "archive")
	dir := filepath.Join(root, "cam_a", "main")
	_ = os.MkdirAll(dir, 0o750)
	for _, f := range []string{"2026-11-09_10-00-00-000000.mp4", "2026-11-09_10-10-00-000000.mp4"} {
		_ = os.WriteFile(filepath.Join(dir, f), buildFMP4(t, 90000, 3000, []fragment{{samples: 30}}), 0o600)
	}
	if _, err := idx.Reconcile(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	rep, err := idx.Reconcile(ctx, root)
	if !errors.Is(err, archive.ErrRootMissing) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrRootMissing wrapping ENOENT", err)
	}
	if rep.Removed != 0 {
		t.Fatalf("report = %+v, want nothing removed", rep)
	}
	segs, _ := idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour))
	if len(segs) != 2 {
		t.Fatalf("index has %d rows after the root vanished, want 2", len(segs))
	}
}

// A hive that has recorded nothing yet has no archive root: that is not an
// error.
func TestNoRootAndNoRowsIsAnEmptyArchive(t *testing.T) {
	idx := archive.NewIndex(openStore(t))
	rep, err := idx.Reconcile(context.Background(), filepath.Join(t.TempDir(), "archive"))
	if err != nil || rep != (archive.Report{}) {
		t.Fatalf("Reconcile = %+v, %v; want nothing and no error", rep, err)
	}
}

// An unreadable root (a file where the directory was, or no permission)
// fails the reconcile too.
func TestAnUnreadableRootKeepsTheIndex(t *testing.T) {
	ctx := context.Background()
	idx := archive.NewIndex(openStore(t))
	root := filepath.Join(t.TempDir(), "archive")
	dir := filepath.Join(root, "cam_a", "main")
	_ = os.MkdirAll(dir, 0o750)
	_ = os.WriteFile(filepath.Join(dir, "2026-11-09_10-00-00-000000.mp4"), buildFMP4(t, 90000, 3000, []fragment{{samples: 30}}), 0o600)
	if _, err := idx.Reconcile(ctx, root); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(root)
	_ = os.WriteFile(root, []byte("not a directory"), 0o600)

	if _, err := idx.Reconcile(ctx, root); !errors.Is(err, archive.ErrRootMissing) {
		t.Fatalf("err = %v, want ErrRootMissing", err)
	}
	if segs, _ := idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour)); len(segs) != 1 {
		t.Fatalf("index has %d rows, want 1", len(segs))
	}
}

// #172 review: the archive disk absent at boot leaves its mount point as
// an empty directory on the system disk. An existing root that holds no
// segment while the index has rows is a missing disk too: nothing empties
// the archive behind the index (retention and deletions remove their rows,
// a disk change goes through move-data).
func TestAnEmptyRootUnderAFullIndexKeepsTheIndex(t *testing.T) {
	ctx := context.Background()
	idx := archive.NewIndex(openStore(t))
	root := filepath.Join(t.TempDir(), "archive")
	dir := filepath.Join(root, "cam_a", "main")
	_ = os.MkdirAll(dir, 0o750)
	_ = os.WriteFile(filepath.Join(dir, "2026-11-09_10-00-00-000000.mp4"), buildFMP4(t, 90000, 3000, []fragment{{samples: 30}}), 0o600)
	if _, err := idx.Reconcile(ctx, root); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(root)
	_ = os.MkdirAll(root, 0o750) // the empty mount point

	if _, err := idx.Reconcile(ctx, root); !errors.Is(err, archive.ErrRootMissing) {
		t.Fatalf("err = %v, want ErrRootMissing", err)
	}
	if segs, _ := idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour)); len(segs) != 1 {
		t.Fatalf("index has %d rows, want 1", len(segs))
	}
}
