package update_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/update"
)

// Security review: the updater's directory must be private to the updater,
// or anyone who can write there runs code as root.
func TestPrivateDirIsCreatedPrivateAndForeignWritableOnesAreRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ACLs are applied, not checked, on Windows")
	}
	dir := filepath.Join(t.TempDir(), "updater")
	if err := update.EnsurePrivate(dir); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	_ = os.Chmod(dir, 0o777) //nolint:gosec // the unsafe state under test
	if err := update.EnsurePrivate(dir); err != nil {
		t.Fatalf("our own directory is repaired, not refused: %v", err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("not repaired: %v", st.Mode().Perm())
	}
	link := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(dir, link)
	if err := update.EnsurePrivate(link); !errors.Is(err, update.ErrUnsafeDir) {
		t.Fatalf("symlink: %v", err)
	}
}

// A version directory planted before the updater installs that version is
// replaced, and a bundle changed after install is not started.
func TestPlantedOrChangedBundlesAreNotTrusted(t *testing.T) {
	dir := t.TempDir()
	s := update.OpenVersions(dir)
	planted := filepath.Join(dir, "versions", "0.1.6")
	if err := writeBundle(planted, "evil"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(v(t, "0.1.6"), bundleDir(t, "good")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s.File(v(t, "0.1.6"), update.BundleFiles[0])); !strings.HasPrefix(got, "good") {
		t.Fatalf("planted bundle kept: %q", got)
	}
	if err := s.Verify(v(t, "0.1.6")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.File(v(t, "0.1.6"), update.BundleFiles[0]), []byte("changed"), 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	if err := s.Verify(v(t, "0.1.6")); err == nil {
		t.Fatal("a changed bundle verified")
	}
}

// A bundle that no longer verifies is not run: the supervisor goes back.
func TestSupervisorWillNotStartAChangedBundle(t *testing.T) {
	hives := &fakeHives{}
	s, vs, events := supervisor(t, hives, &fakeSource{err: errors.New("offline")})
	if err := vs.Add(v(t, "0.1.5"), bundleDir(t, "x")); err != nil {
		t.Fatal(err)
	}
	if err := vs.Switch(v(t, "0.1.5")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(vs.File(v(t, "0.1.5"), update.BundleFiles[0]), []byte("changed"), 0o755) //nolint:gosec // test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return hives.runningVersion() == "0.1.4" })
	if count(hives, "0.1.5") != 0 {
		t.Fatal("started a changed bundle")
	}
	eventually(t, func() bool { e := events(); return len(e) > 0 && e[0] == "update.integrity_failed" })
}

// With the clock before the updater was built (a Pi without a clock
// battery before NTP), metadata expiry means nothing: no check.
func TestNoCheckWhileTheClockIsImplausible(t *testing.T) {
	hives := &fakeHives{}
	src := &countingSource{fakeSource: fakeSource{target: update.Target{Version: v(t, "0.1.5"), Rollout: 100}}}
	s, _, _ := supervisorWith(t, hives, src, func(c *update.SupervisorConfig) {
		c.NotBefore = time.Now().Add(time.Hour)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return hives.runningVersion() == "0.1.4" })
	s.CheckNow(ctx)
	if src.checks != 0 {
		t.Fatalf("checked %d times with an implausible clock", src.checks)
	}
}

type countingSource struct {
	fakeSource
	checks int
}

func (c *countingSource) Check(ctx context.Context) (update.Target, error) {
	c.checks++
	return c.fakeSource.Check(ctx)
}
