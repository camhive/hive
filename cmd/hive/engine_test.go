package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
)

// #62: release archives put mediamtx next to hive; deb and rpm install both
// in /usr/lib/camhive and link /usr/bin/hive there. The engine must be found
// either way, and nothing found means "search PATH" (development).
func TestEngineIsFoundNextToTheHiveEvenThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; the archive layout is covered on Linux")
	}
	root := t.TempDir()
	lib, bin := filepath.Join(root, "lib", "camhive"), filepath.Join(root, "bin")
	for _, d := range []string{lib, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hive, engine := filepath.Join(lib, "hive"), filepath.Join(lib, "mediamtx")
	_ = os.WriteFile(hive, []byte("x"), 0o755)
	link := filepath.Join(bin, "hive")
	if err := os.Symlink(hive, link); err != nil {
		t.Fatal(err)
	}

	if got := mediamtxNextTo(hive); got != "" {
		t.Fatalf("no engine yet, got %q", got)
	}
	_ = os.WriteFile(engine, []byte("x"), 0o755)
	if got := mediamtxNextTo(hive); got != engine {
		t.Fatalf("archive layout: %q", got)
	}
	if got := mediamtxNextTo(link); got != engine {
		t.Fatalf("package layout through /usr/bin/hive: %q", got)
	}
	if got := mediamtxNextTo(filepath.Join(root, "missing", "hive")); got != "" {
		t.Fatalf("unresolvable path: %q", got)
	}
}

// #61: the engine listens on the ports hive.yaml names.
func TestEngineTakesItsPortsFromTheConfig(t *testing.T) {
	cfg := hiveconfig.Config{ArchiveDir: "/a", SegmentMinutes: 2, EngineAPIPort: 19997, EngineWebRTCPort: 18889, EngineWebRTCUDPPort: 18900,
		ICEServers: []string{"stun:stun.example:3478"}}
	c := engineConfig(cfg, hiveconfig.Paths{Data: "/d"})
	if c.APIPort != 19997 || c.WebRTCPort != 18889 || c.WebRTCUDPPort != 18900 {
		t.Fatalf("ports = %d %d %d", c.APIPort, c.WebRTCPort, c.WebRTCUDPPort)
	}
	if c.ArchiveDir != "/a" || c.SegmentDuration != 2*time.Minute || c.WorkDir != filepath.Join("/d", "engine") || len(c.ICEServers) != 1 || c.ICEServers[0].URL != "stun:stun.example:3478" {
		t.Fatalf("config = %+v", c)
	}
}
