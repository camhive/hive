package hiveconfig_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
)

// #150: the app changes retention_days and min_free_gb in hive.yaml,
// keeping comments and every other setting; the values stay numbers.
func TestSaveStorageSettingsEditsHiveYAMLInPlace(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	hand := "# my hive\nretention_days: 14 # two weeks\nupdates: manual\n"
	if err := os.WriteFile(paths.Config, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	want := hiveconfig.StorageSettings{RetentionDays: 60, MinFreeGB: 25}
	if err := hiveconfig.SaveStorageSettings(paths, want); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil || cfg.StorageSettings() != want || cfg.Updates != "manual" {
		t.Fatalf("after save: %+v %v", cfg, err)
	}
	raw, _ := os.ReadFile(paths.Config)
	if !strings.Contains(string(raw), "# my hive") || !strings.Contains(string(raw), "retention_days: 60 # two weeks") || !strings.Contains(string(raw), "min_free_gb: 25\n") {
		t.Fatalf("hive.yaml:\n%s", raw)
	}
}

func TestSaveStorageSettingsRefusesNegativeValues(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil { // writes the defaults
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.Config)
	for _, bad := range []hiveconfig.StorageSettings{{RetentionDays: -1, MinFreeGB: 10}, {RetentionDays: 30, MinFreeGB: -1}} {
		if err := hiveconfig.SaveStorageSettings(paths, bad); !errors.Is(err, hiveconfig.ErrStorageSettings) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if after, _ := os.ReadFile(paths.Config); string(after) != string(before) {
		t.Fatalf("a refused change was written:\n%s", after)
	}
}

// #155: hive move-data points the copied hive.yaml at the archive on the
// new disk, keeping everything else.
func TestSaveArchiveDirEditsHiveYAMLInPlace(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	hand := "# my hive\narchive_dir: /mnt/old/archive # the big disk\nretention_days: 14\n"
	if err := os.WriteFile(paths.Config, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hiveconfig.SaveArchiveDir(paths, "/mnt/new/camhive/archive"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil || cfg.ArchiveDir != "/mnt/new/camhive/archive" || cfg.RetentionDays != 14 {
		t.Fatalf("after save: %+v %v", cfg, err)
	}
	if raw, _ := os.ReadFile(paths.Config); !strings.Contains(string(raw), "# the big disk") {
		t.Fatalf("hive.yaml lost its comment:\n%s", raw)
	}
}
