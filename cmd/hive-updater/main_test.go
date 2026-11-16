package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/update/journal"
	"github.com/camhive/hive/internal/version"
)

func bundle(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range update.BundleFiles {
		if err := os.WriteFile(filepath.Join(d, n), []byte(n), 0o755); err != nil { //nolint:gosec // test executables
			t.Fatal(err)
		}
	}
	return d
}

// The packaged bundle next to the executable becomes the first version in
// a private store; a release build checks the default channel with the
// built-in root.
func TestAssembleSeedsThePackagedBundleIntoAPrivateStore(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "updater")
	sup, err := assemble(log, paths, dir, bundle(t), version.Info{Version: "0.1.5", Date: "2026-11-13"})
	if err != nil || sup == nil {
		t.Fatal(err)
	}
	if cur, _ := update.OpenVersions(dir).Current(); cur.String() != "0.1.5" {
		t.Fatalf("current = %v", cur)
	}
	if st, _ := os.Stat(dir); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("store mode = %v", st.Mode().Perm())
	}
	if v, _ := journal.Announced(dir); v != "0.1.5" { // reads hive.yaml at every check (#82)
		t.Fatalf("announced = %q", v)
	}
	if !strings.Contains(logs.String(), "url=https://updates.dev.camhive.app/stable") {
		t.Fatalf("logs = %s", logs.String())
	}
}

func TestDevelopmentBuildRunsWithoutUpdates(t *testing.T) {
	var logs bytes.Buffer
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "updater")
	if _, err := assemble(slog.New(slog.NewTextHandler(&logs, nil)), paths, dir, bundle(t), version.Info{}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := update.OpenVersions(dir).Current(); cur.String() != "0.0.0-dev" || !strings.Contains(logs.String(), "development build") {
		t.Fatalf("current = %v, logs = %s", cur, logs.String())
	}
}

func TestAssembleRefusesAPackageWithoutItsFiles(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	if _, err := assemble(slog.New(slog.DiscardHandler), paths, filepath.Join(t.TempDir(), "u"), t.TempDir(), version.Info{Version: "0.1.5"}); err == nil {
		t.Fatal("seeded from an empty directory")
	}
}

func TestVersionCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "hive-updater") {
		t.Fatalf("%d %q", code, out.String())
	}
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestDataPathsFollowCAMHIVE_DATA_DIR(t *testing.T) {
	def := hiveconfig.DefaultPathsFor("linux", func(string) string { return "" })
	if got := dataPaths(def, ""); got != def {
		t.Fatalf("no override: %+v", got)
	}
	if got := dataPaths(def, "/media/usb/camhive"); got.Config != filepath.Join("/media/usb/camhive", "hive.yaml") {
		t.Fatalf("override: %+v", got)
	}
}

// The updater tells the hive it starts whether it updates it (#82).
func TestLaunchVerdict(t *testing.T) {
	for _, c := range []struct {
		release, source bool
		want            string
	}{{true, true, "on"}, {false, true, "dev_build"}, {false, false, "dev_build"}, {true, false, "no_root"}} {
		if got := launchVerdict(c.release, c.source); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}
