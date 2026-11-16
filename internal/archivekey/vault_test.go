package archivekey_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
)

const (
	pass  = "correct horse battery"
	other = "another long passphrase"
)

// sealed writes plain bytes sealed to the vault's recipient as name.mp4.age.
func sealed(t *testing.T, v *archivekey.Vault, plain []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "2026-11-16_08-00-00-000000_Z.mp4")
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := archive.SealFile(path, v.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readAllAt(t *testing.T, f archive.SegmentFile) []byte {
	t.Helper()
	got, err := io.ReadAll(io.NewSectionReader(f, 0, f.Size()))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEncryptionIsOffUntilTheOwnerEnablesIt(t *testing.T) {
	v := archivekey.New(t.TempDir())
	if v.State() != archivekey.Off || v.Recipient() != nil {
		t.Fatalf("state %s, recipient %v; want off and none", v.State(), v.Recipient())
	}
	for name, err := range map[string]error{"unlock": v.Unlock(pass), "change": v.ChangePassphrase(pass, other)} {
		if !errors.Is(err, archivekey.ErrOff) {
			t.Errorf("%s: %v, want ErrOff", name, err)
		}
	}
	if _, err := v.KeyFile(); !errors.Is(err, archivekey.ErrOff) {
		t.Errorf("key file: %v, want ErrOff", err)
	}
}

func TestEnablingLeavesTheArchiveUnlockedAndRefusesASecondTime(t *testing.T) {
	v := archivekey.New(t.TempDir())
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	if v.State() != archivekey.Unlocked || v.Recipient() == nil {
		t.Fatalf("state %s after enabling", v.State())
	}
	if err := v.Enable(other); !errors.Is(err, archivekey.ErrOn) {
		t.Fatalf("second enable: %v, want ErrOn", err)
	}
}

func TestAWeakPassphraseIsRefused(t *testing.T) {
	v := archivekey.New(t.TempDir())
	for _, p := range []string{"", "short", string(bytes.Repeat([]byte("x"), auth.MaxPasswordLen+1))} {
		if err := v.Enable(p); !errors.Is(err, archivekey.ErrWeakPassphrase) {
			t.Errorf("%d characters: %v, want ErrWeakPassphrase", len(p), err)
		}
	}
	if v.State() != archivekey.Off {
		t.Fatal("a refused passphrase enabled encryption")
	}
}

// A restarted hive (a new Vault over the same directory) can seal new
// footage at once but reads none until the owner unlocks it.
func TestAfterARestartTheArchiveIsLockedUntilUnlocked(t *testing.T) {
	dir := t.TempDir()
	first := archivekey.New(dir)
	if err := first.Enable(pass); err != nil {
		t.Fatal(err)
	}
	restarted := archivekey.New(dir)
	if restarted.State() != archivekey.Locked {
		t.Fatalf("state %s after a restart, want locked", restarted.State())
	}
	path := sealed(t, restarted, []byte("footage"))
	if _, err := restarted.Open(path); !errors.Is(err, archive.ErrLocked) {
		t.Fatalf("open while locked: %v, want ErrLocked", err)
	}
	if err := restarted.Unlock(other); !errors.Is(err, archivekey.ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if err := restarted.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	f, err := restarted.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if got := readAllAt(t, f); string(got) != "footage" {
		t.Fatalf("read %q", got)
	}
}

func TestLockingForgetsTheKey(t *testing.T) {
	v := archivekey.New(t.TempDir())
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	path := sealed(t, v, []byte("footage"))
	v.Lock()
	if v.State() != archivekey.Locked {
		t.Fatalf("state %s after lock", v.State())
	}
	if _, err := v.Open(path); !errors.Is(err, archive.ErrLocked) {
		t.Fatalf("open after lock: %v, want ErrLocked", err)
	}
}

// Plain files (recorded before encryption was on, or the segment being
// written) open whatever the state.
func TestPlainFilesOpenWhileLocked(t *testing.T) {
	dir := t.TempDir()
	if err := archivekey.New(dir).Enable(pass); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plain.mp4")
	if err := os.WriteFile(path, []byte("clear"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := archivekey.New(dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if got := readAllAt(t, f); string(got) != "clear" {
		t.Fatalf("read %q", got)
	}
}

func TestChangingThePassphraseKeepsTheFootageReadable(t *testing.T) {
	dir := t.TempDir()
	v := archivekey.New(dir)
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	path := sealed(t, v, []byte("footage"))
	if err := v.ChangePassphrase(other, "a third passphrase"); !errors.Is(err, archivekey.ErrWrongPassphrase) {
		t.Fatalf("change with the wrong passphrase: %v", err)
	}
	if err := v.ChangePassphrase(pass, "short"); !errors.Is(err, archivekey.ErrWeakPassphrase) {
		t.Fatalf("change to a weak passphrase: %v", err)
	}
	if err := v.ChangePassphrase(pass, other); err != nil {
		t.Fatal(err)
	}
	restarted := archivekey.New(dir)
	if err := restarted.Unlock(pass); !errors.Is(err, archivekey.ErrWrongPassphrase) {
		t.Fatalf("old passphrase after the change: %v", err)
	}
	if err := restarted.Unlock(other); err != nil {
		t.Fatal(err)
	}
	f, err := restarted.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// The key file the owner downloads is a standard age file: with the
// passphrase, the stock age tool recovers the keys, and they open the
// footage without Camhive.
func TestTheKeyFileOpensTheFootageWithStockAge(t *testing.T) {
	v := archivekey.New(t.TempDir())
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	path := sealed(t, v, []byte("footage"))
	keyFile, err := v.KeyFile()
	if err != nil {
		t.Fatal(err)
	}
	scrypt, err := age.NewScryptIdentity(pass)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := age.Decrypt(bytes.NewReader(keyFile), scrypt)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := age.ParseIdentities(keys)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	plain, err := age.Decrypt(enc, ids...)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(plain); string(got) != "footage" {
		t.Fatalf("read %q", got)
	}
}

// A copy of the disk without the passphrase opens no segment: neither the
// recipient file nor anything else on it is a key.
func TestADiskCopyWithoutThePassphraseOpensNothing(t *testing.T) {
	dir := t.TempDir()
	v := archivekey.New(dir)
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	path := sealed(t, v, []byte("footage"))
	var ids []age.Identity
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p) //nolint:gosec // the test's own temp dir
		if err != nil {
			return err
		}
		if found, err := age.ParseIdentities(bytes.NewReader(raw)); err == nil {
			ids = append(ids, found...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("the disk holds %d usable keys in clear", len(ids))
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil || bytes.Contains(raw, []byte("footage")) {
		t.Fatalf("the sealed file holds its content in clear (%v)", err)
	}
	if _, err := archivekey.New(dir).Open(path); !errors.Is(err, archive.ErrLocked) {
		t.Fatalf("open from a copied disk: %v, want ErrLocked", err)
	}
}

func TestGuessingThePassphraseIsSlowedDown(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	if err := archivekey.New(dir).Enable(pass); err != nil {
		t.Fatal(err)
	}
	v := archivekey.New(dir, archivekey.WithClock(func() time.Time { return now }))
	for range archivekey.MaxFailures {
		if err := v.Unlock(other); !errors.Is(err, archivekey.ErrWrongPassphrase) {
			t.Fatalf("guess: %v", err)
		}
	}
	var limited *archivekey.RateLimitedError
	if err := v.Unlock(pass); !errors.As(err, &limited) || !limited.Until.After(now) {
		t.Fatalf("after %d failures: %v, want a rate limit", archivekey.MaxFailures, err)
	}
	now = limited.Until
	if err := v.Unlock(pass); err != nil {
		t.Fatalf("after the lock ended: %v", err)
	}
}

// A passphrase change that cannot be written leaves the old passphrase
// working and the keyring as it was.
func TestAFailedPassphraseChangeKeepsTheOldOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	dir := t.TempDir()
	v := archivekey.New(dir)
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // the test's own directory, made read-only
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // as above
	if err := v.ChangePassphrase(pass, other); err == nil {
		t.Fatal("a passphrase change into a read-only keyring succeeded")
	}
	if err := archivekey.New(dir).Unlock(pass); err != nil {
		t.Fatalf("the old passphrase after a failed change: %v", err)
	}
}

// The keyring is the "encryption on" marker and is written last: a public
// key that cannot be written leaves encryption off, not "on" with nothing
// to seal to.
func TestAnEnableThatCannotWriteThePublicKeyLeavesEncryptionOff(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "recipient.txt")
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil { // a rename cannot replace a non-empty directory
		t.Fatal(err)
	}
	v := archivekey.New(dir)
	if err := v.Enable(pass); err == nil {
		t.Fatal("enable succeeded without writing the public key")
	}
	if v.State() != archivekey.Off || archivekey.New(dir).State() != archivekey.Off {
		t.Fatalf("state %s after a failed enable, want off", v.State())
	}
}

// A public key left by an enable cut off before its keyring is replaced
// by the next enable.
func TestAnEnableCutOffBeforeTheKeyringCanBeRepeated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "recipient.txt"), []byte("age1leftover\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := archivekey.New(dir)
	if v.State() != archivekey.Off {
		t.Fatalf("a public key alone: %s, want off", v.State())
	}
	if err := v.Enable(pass); err != nil || !v.Sealing() {
		t.Fatalf("enable: %v, sealing %v", err, v.Sealing())
	}
}

// A keyring whose public key is missing or unreadable cannot seal new
// footage: the vault says so (Sealing false, an error in the log) instead
// of looking locked and fine, and unlocking writes the public key again.
func TestAKeyringWithoutItsPublicKeySaysSealingIsOff(t *testing.T) {
	dir := t.TempDir()
	if err := archivekey.New(dir).Enable(pass); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "recipient.txt")); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	v := archivekey.New(dir, archivekey.WithLogger(slog.New(slog.NewTextHandler(&logged, nil))))
	if v.State() != archivekey.Locked || v.Sealing() || v.Recipient() != nil {
		t.Fatalf("state %s, sealing %v; want locked and not sealing", v.State(), v.Sealing())
	}
	if !strings.Contains(logged.String(), "level=ERROR") {
		t.Fatalf("nothing logged: %q", logged.String())
	}
	if err := v.Unlock(pass); err != nil || !v.Sealing() {
		t.Fatalf("unlock: %v, sealing %v", err, v.Sealing())
	}
	if !archivekey.New(dir).Sealing() {
		t.Fatal("unlocking did not write the public key again")
	}
}

// Changing the passphrase does not unlock a locked archive.
func TestAPassphraseChangeKeepsALockedArchiveLocked(t *testing.T) {
	dir := t.TempDir()
	if err := archivekey.New(dir).Enable(pass); err != nil {
		t.Fatal(err)
	}
	v := archivekey.New(dir)
	if err := v.ChangePassphrase(pass, other); err != nil {
		t.Fatal(err)
	}
	if v.State() != archivekey.Locked {
		t.Fatalf("state %s after a passphrase change, want locked", v.State())
	}
}

// Two changes from the same current passphrase at once: one wins, the
// other finds the passphrase changed, and the keyring opens with the
// winner's.
func TestConcurrentPassphraseChangesDoNotBothSucceed(t *testing.T) {
	dir := t.TempDir()
	v := archivekey.New(dir)
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	next := []string{"first new passphrase", "second new passphrase"}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range next {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = v.ChangePassphrase(pass, next[i]) }()
	}
	wg.Wait()
	won := -1
	for i, err := range errs {
		switch {
		case err == nil && won < 0:
			won = i
		case err == nil:
			t.Fatal("both changes succeeded")
		case !errors.Is(err, archivekey.ErrWrongPassphrase):
			t.Fatalf("change %d: %v", i, err)
		}
	}
	if won < 0 {
		t.Fatal("neither change succeeded")
	}
	if err := archivekey.New(dir).Unlock(next[won]); err != nil {
		t.Fatalf("the winner's passphrase: %v", err)
	}
}
