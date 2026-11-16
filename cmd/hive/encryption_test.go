package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
)

// An encrypted archive records 2-minute segments at most, so little footage
// waits in clear (ADR-0019 §2); without encryption the configured length holds.
func TestEncryptedStreamsRecordShortSegments(t *testing.T) {
	cases := []struct {
		configured int
		state      archivekey.State
		want       int
	}{
		{10, archivekey.Off, 10},
		{10, archivekey.Locked, 2},
		{10, archivekey.Unlocked, 2},
		{1, archivekey.Locked, 1},
	}
	for _, c := range cases {
		if got := segmentMinutes(c.configured, c.state); got != c.want {
			t.Errorf("%d minutes, %s: %d, want %d", c.configured, c.state, got, c.want)
		}
	}
}

func init() { archivekey.WorkFactor = 10 } // fast scrypt in tests

const testPassphrase = "correct horse battery"

type fakeSegmentLength struct{ got []time.Duration }

func (f *fakeSegmentLength) SetSegmentDuration(_ context.Context, d time.Duration) error {
	f.got = append(f.got, d)
	return nil
}

// Switching encryption on shortens the running engine's segments at once;
// a refused switch changes nothing; without an engine it still switches.
func TestEnablingEncryptionShortensTheEnginesSegments(t *testing.T) {
	eng := &fakeSegmentLength{}
	k := keyWithEngine{Vault: archivekey.New(t.TempDir()), eng: eng, log: slog.New(slog.DiscardHandler)}
	if err := k.Enable("short"); err == nil || len(eng.got) != 0 {
		t.Fatalf("weak passphrase: %v, engine told %v", err, eng.got)
	}
	if err := k.Enable(testPassphrase); err != nil || len(eng.got) != 1 || eng.got[0] != archivekey.SegmentDuration {
		t.Fatalf("enable: %v, engine told %v", err, eng.got)
	}
	noEngine := keyWithEngine{Vault: archivekey.New(t.TempDir()), log: slog.New(slog.DiscardHandler)}
	if err := noEngine.Enable(testPassphrase); err != nil {
		t.Fatal(err)
	}
}

func TestTheArchiveKeyOpensLockedAfterARestart(t *testing.T) {
	paths := hiveconfig.Paths{Data: t.TempDir()}
	log := slog.New(slog.DiscardHandler)
	if v := openArchiveKey(paths, log); v.State() != archivekey.Off {
		t.Fatalf("fresh hive: %s", v.State())
	} else if err := v.Enable(testPassphrase); err != nil {
		t.Fatal(err)
	}
	if v := openArchiveKey(paths, log); v.State() != archivekey.Locked {
		t.Fatalf("after a restart: %s", v.State())
	}
}

// One sealing pass seals closed segments while encryption is on, and
// nothing while it is off.
func TestASealingPassRunsOnlyWhileEncryptionIsOn(t *testing.T) {
	ctx := context.Background()
	root, log := t.TempDir(), slog.New(slog.DiscardHandler)
	dir := filepath.Join(root, "cam_1", "main")
	_ = os.MkdirAll(dir, 0o750)
	for _, name := range []string{"2026-11-16_08-00-00-000000_Z.mp4", "2026-11-16_08-02-00-000000_Z.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("footage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	vault := archivekey.New(t.TempDir())
	idx := archive.NewIndex(st, archive.WithOpener(vault.OpenSegment))
	exports := export.NewManager(t.TempDir(), nil, st.Exports(), export.WithVault(vault))

	sealOnce(ctx, vault, idx, root, exports, log)
	if sealed := countSealedIn(t, dir); sealed != 0 {
		t.Fatalf("sealed %d with encryption off", sealed)
	}
	if err := vault.Enable(testPassphrase); err != nil {
		t.Fatal(err)
	}
	sealOnce(ctx, vault, idx, root, exports, log)
	if sealed := countSealedIn(t, dir); sealed != 1 {
		t.Fatalf("sealed %d, want the closed one (the newest is being written)", sealed)
	}
}

func countSealedIn(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if archive.IsSealed(e.Name()) {
			n++
		}
	}
	return n
}
