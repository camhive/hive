package update_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/update"
)

func v(t *testing.T, s string) update.Version {
	t.Helper()
	x, err := update.ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

// bundleDir makes a directory with the files of a bundle.
func bundleDir(t *testing.T, tag string) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range update.BundleFiles {
		if err := os.WriteFile(filepath.Join(d, n), []byte(tag+" "+n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ADR-0018 §5: the packaged version seeds the store; a newer package is
// selected, an older one never replaces a newer running version.
func TestSeedSelectsThePackageUnlessANewerVersionRuns(t *testing.T) {
	s := update.OpenVersions(t.TempDir())
	if _, err := s.Current(); !errors.Is(err, update.ErrNoVersion) {
		t.Fatalf("empty store: %v", err)
	}
	if err := s.Seed(v(t, "0.1.4"), bundleDir(t, "pkg4")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.Current(); cur.String() != "0.1.4" || !strings.HasPrefix(read(t, s.File(cur, update.BundleFiles[0])), "pkg4") {
		t.Fatalf("current = %v", cur)
	}
	if err := s.Seed(v(t, "0.1.6"), bundleDir(t, "pkg6")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.Current(); cur.String() != "0.1.6" {
		t.Fatalf("a newer package was not selected: %v", cur)
	}
	if err := s.Seed(v(t, "0.1.5"), bundleDir(t, "pkg5")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.Current(); cur.String() != "0.1.6" {
		t.Fatalf("an older package downgraded: %v", cur)
	}
	// Seeding the same version again copies the package's files again: the
	// store follows the package, it never trusts what it already holds.
	if err := s.Seed(v(t, "0.1.6"), bundleDir(t, "repkg6")); err != nil || !strings.HasPrefix(read(t, s.File(v(t, "0.1.6"), update.BundleFiles[0])), "repkg6") {
		t.Fatalf("re-seed: %v", err)
	}
	if cur, _ := s.Current(); cur.String() != "0.1.6" || s.Verify(cur) != nil {
		t.Fatalf("after re-seed: %v", cur)
	}
}

// Switching remembers the previous version; a rollback goes back to it and
// marks the bad one as failed for good; old versions are pruned.
func TestSwitchRollbackAndPrune(t *testing.T) {
	dir := t.TempDir()
	s := update.OpenVersions(dir)
	for _, ver := range []string{"0.1.4", "0.1.5", "0.1.6"} {
		if err := s.Add(v(t, ver), bundleDir(t, ver)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Switch(v(t, "0.1.4")); err != nil {
		t.Fatal(err)
	}
	if err := s.Switch(v(t, "0.1.5")); err != nil {
		t.Fatal(err)
	}
	back, err := s.Rollback()
	if err != nil || back.String() != "0.1.4" {
		t.Fatalf("rollback = %v, %v", back, err)
	}
	if cur, _ := s.Current(); cur.String() != "0.1.4" {
		t.Fatalf("current after rollback = %v", cur)
	}
	failed := update.OpenVersions(dir).Failed() // survives a restart
	if len(failed) != 1 || failed[0].String() != "0.1.5" {
		t.Fatalf("failed = %v", failed)
	}
	if err := s.Switch(v(t, "0.1.6")); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(filepath.Join(dir, "versions"))
	names := make([]string, 0, len(left))
	for _, e := range left {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "0.1.4,0.1.6" { // current and the one to roll back to
		t.Fatalf("kept %v", names)
	}
	if _, err := s.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rollback(); !errors.Is(err, update.ErrNoVersion) {
		t.Fatalf("second rollback: %v", err)
	}
	if err := s.Switch(v(t, "9.9.9")); err == nil {
		t.Fatal("switched to a version that is not there")
	}
}

// An unfinished copy (a crash while adding) is not a version.
func TestHalfAddedVersionIsIgnoredAndRedone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "versions", "0.1.7.partial"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := update.OpenVersions(dir)
	if err := s.Add(v(t, "0.1.7"), bundleDir(t, "x")); err != nil {
		t.Fatal(err)
	}
	if !s.Has(v(t, "0.1.7")) || s.Has(v(t, "0.1.8")) {
		t.Fatal("Has")
	}
	if err := s.Add(v(t, "0.1.8"), t.TempDir()); err == nil || s.Has(v(t, "0.1.8")) {
		t.Fatal("a bundle without its files was added")
	}
}
