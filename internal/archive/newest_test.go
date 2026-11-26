package archive_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
)

// #172: the recording watchdog asks for the newest segment's start under
// the archive, from the file names alone.
func TestNewestIsTheLatestSegmentStartOfAnyStream(t *testing.T) {
	root := t.TempDir()
	for dir, files := range map[string][]string{
		"cam_a/main": {"2026-11-24_10-00-00-000000.mp4.age", "2026-11-24_10-10-00-000000.mp4", "junk.txt"},
		"cam_a/sub":  {"2026-11-24_10-20-00-500000_+0100.mp4"}, // 09:20:00.5 UTC
		"cam_b/main": {"2026-11-24_10-15-00-000000_Z.mp4"},
		"cam_b/sub":  {},
	} {
		_ = os.MkdirAll(filepath.Join(root, dir), 0o750)
		for _, f := range files {
			_ = os.WriteFile(filepath.Join(root, dir, f), nil, 0o600)
		}
	}
	got, err := archive.Newest(root)
	if want := time.Date(2026, 11, 24, 10, 15, 0, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Fatalf("Newest = %v, %v; want %v", got, err, want)
	}
}

func TestNewestOfAnEmptyOrMissingArchive(t *testing.T) {
	if got, err := archive.Newest(t.TempDir()); err != nil || !got.IsZero() {
		t.Fatalf("empty archive: %v, %v; want zero, nil", got, err)
	}
	if _, err := archive.Newest(filepath.Join(t.TempDir(), "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing archive: err = %v, want ErrNotExist", err)
	}
}
