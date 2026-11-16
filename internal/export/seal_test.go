package export_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/export"
)

// fakeVault is the archive key as the export manager sees it.
type fakeVault struct {
	key        *age.X25519Identity
	on, locked bool
}

func newVault(t *testing.T, on bool) *fakeVault {
	t.Helper()
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeVault{key: key, on: on}
}

func (v *fakeVault) Recipient() age.Recipient {
	if !v.on {
		return nil
	}
	return v.key.Recipient()
}

func (v *fakeVault) Open(path string) (archive.SegmentFile, error) {
	if !archive.IsSealed(path) {
		return archive.OpenFile(path)
	}
	if v.locked {
		return nil, archive.ErrLocked
	}
	return archive.OpenSealed(path, v.key)
}

var exportReq = export.Request{Camera: "cam_a", Stream: "main", Start: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Duration: 2 * time.Minute, Owner: "acc_1"}

func download(t *testing.T, m *export.Manager, id string) []byte {
	t.Helper()
	f, err := m.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.NewSectionReader(f, 0, f.Size()))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// With encryption on, an export never lies on disk in clear: it is written
// sealed, counted at its sealed size, and the owner downloads plain MP4.
func TestAnEncryptedExportIsStoredSealedAndDownloadsPlain(t *testing.T) {
	dir, v := t.TempDir(), newVault(t, true)
	m := export.NewManager(dir, fetchOK, jobs(t), export.WithVault(v))
	job, err := m.Start(ctx, exportReq)
	if err != nil {
		t.Fatal(err)
	}
	final := waitStatus(t, m, job.ID, export.StatusDone)

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".mp4"+archive.SealedExt) {
		t.Fatalf("export dir holds %v, want one sealed file", entries)
	}
	raw, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if bytes.Contains(raw, []byte("ftyp")) || final.Bytes != int64(len(raw)) {
		t.Fatalf("stored file: %d bytes, plain MP4 inside: %v; job counts %d", len(raw), bytes.Contains(raw, []byte("ftyp")), final.Bytes)
	}
	if data := download(t, m, job.ID); len(data) != 1012 || string(data[4:8]) != "ftyp" {
		t.Fatalf("download: %d bytes", len(data))
	}
	v.locked = true
	if _, err := m.Open(ctx, job.ID); !errors.Is(err, archive.ErrLocked) {
		t.Fatalf("download while locked: %v, want ErrLocked", err)
	}
}

// Exports made before encryption was switched on are sealed afterwards.
func TestSealingSealsExportsKeptInClear(t *testing.T) {
	dir, v := t.TempDir(), newVault(t, false)
	m := export.NewManager(dir, fetchOK, jobs(t), export.WithVault(v))
	job, _ := m.Start(ctx, exportReq)
	waitStatus(t, m, job.ID, export.StatusDone)
	v.on = true

	if n, err := m.Seal(ctx, v.Recipient()); err != nil || n != 1 {
		t.Fatalf("sealed %d, %v; want 1", n, err)
	}
	if n, err := m.Seal(ctx, v.Recipient()); err != nil || n != 0 {
		t.Fatalf("second pass sealed %d, %v", n, err)
	}
	path, _ := m.FilePath(ctx, job.ID)
	st, _ := os.Stat(path)
	got, _ := m.Get(ctx, job.ID)
	if !archive.IsSealed(path) || st == nil || got.Bytes != st.Size() {
		t.Fatalf("stored %s, job counts %d bytes", path, got.Bytes)
	}
	if data := download(t, m, job.ID); len(data) != 1012 {
		t.Fatalf("download: %d bytes", len(data))
	}
}

func TestRemovingASealedExportRemovesItsFile(t *testing.T) {
	dir := t.TempDir()
	m := export.NewManager(dir, fetchOK, jobs(t), export.WithVault(newVault(t, true)))
	job, _ := m.Start(ctx, exportReq)
	waitStatus(t, m, job.ID, export.StatusDone)
	all := func(string, time.Time, time.Time) bool { return true }
	if _, err := m.Remove(ctx, all, false); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}
