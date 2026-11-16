package hiveconfig_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
)

// #82: the app changes the update settings in hive.yaml itself, keeping
// the owner's comments and every other setting.
func TestSaveUpdateSettingsEditsHiveYAMLInPlace(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	hand := "# my hive\nretention_days: 14 # two weeks\nice_servers:\n  - stun:stun.example.de\nupdates: automatic\n"
	if err := os.WriteFile(paths.Config, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	want := hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "22:00-23:30", UpdateChannel: "beta"}
	if err := hiveconfig.SaveUpdateSettings(paths, want); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil || cfg.UpdateSettings() != want || cfg.RetentionDays != 14 {
		t.Fatalf("after save: %+v %v", cfg, err)
	}
	raw, _ := os.ReadFile(paths.Config)
	if !strings.Contains(string(raw), "# my hive") || !strings.Contains(string(raw), "# two weeks") || !strings.Contains(string(raw), "\n  - stun:stun.example.de\n") {
		t.Fatalf("comments lost:\n%s", raw)
	}
}

// Validated like hive.yaml; a refused change leaves the file as it was.
func TestSaveUpdateSettingsRefusesWhatHiveYAMLRefuses(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil { // writes the defaults
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.Config)
	for _, bad := range []struct {
		s      hiveconfig.UpdateSettings
		window bool
	}{
		{hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "25:00-03:00", UpdateChannel: "stable"}, true},
		{hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "03:00-03:00", UpdateChannel: "stable"}, true},
		{hiveconfig.UpdateSettings{Updates: "sometimes", UpdateWindow: "02:00-05:00", UpdateChannel: "stable"}, false},
		{hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "02:00-05:00", UpdateChannel: "nightly"}, false},
	} {
		err := hiveconfig.SaveUpdateSettings(paths, bad.s)
		if err == nil || errors.Is(err, hiveconfig.ErrUpdateWindow) != bad.window {
			t.Errorf("%+v: err = %v", bad.s, err)
		}
	}
	if after, _ := os.ReadFile(paths.Config); string(after) != string(before) {
		t.Fatalf("a refused change wrote hive.yaml:\n%s", after)
	}
}
