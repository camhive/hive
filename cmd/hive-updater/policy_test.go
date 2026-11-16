package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
)

// #82: the updater reads mode, window and channel from hive.yaml at every
// check, so what the owner changed in the app applies without a restart.
func TestPolicyFollowsHiveYAMLAtEveryCheck(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	dir := filepath.Join(t.TempDir(), "updater")
	if _, _, err := hiveconfig.Load(paths); err != nil { // the defaults, as the hive and assemble write them
		t.Fatal(err)
	}
	pol := newPolicies(paths, dir, true, slog.New(slog.DiscardHandler))
	p := pol.current()
	if src, _ := p.Source.(*update.SourceChannel); p.Mode != update.Automatic || src == nil || src.Source.MetadataURL != "https://updates.dev.camhive.app/stable" || src.Source.CacheDir != filepath.Join(dir, "tuf") {
		t.Fatalf("default policy = %+v", p)
	}

	if err := hiveconfig.SaveUpdateSettings(paths, hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "22:00-23:00", UpdateChannel: "beta"}); err != nil {
		t.Fatal(err)
	}
	p = pol.current()
	src, _ := p.Source.(*update.SourceChannel)
	at := func(hm string) time.Time { t, _ := time.Parse("15:04", hm); return t }
	if p.Mode != update.Manual || src == nil || src.Source.MetadataURL != "https://updates.dev.camhive.app/beta" || !p.Window.Contains(at("22:30")) || p.Window.Contains(at("02:30")) {
		t.Fatalf("changed policy = %+v", p)
	}
	if src.Source.CacheDir != filepath.Join(dir, "tuf-beta") {
		t.Fatalf("each channel keeps its own trusted metadata, got %s", src.Source.CacheDir)
	}

	// A broken hand edit keeps the last good policy instead of stopping updates.
	if err := os.WriteFile(paths.Config, []byte("updates: sometimes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := pol.current(); p.Mode != update.Manual || p.Source == nil {
		t.Fatalf("after a broken edit = %+v", p)
	}
}

func TestDevelopmentBuildHasNoSourceWhateverTheSettings(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	if p := newPolicies(paths, t.TempDir(), false, slog.New(slog.DiscardHandler)).current(); p.Source != nil {
		t.Fatalf("dev build policy = %+v", p)
	}
}
