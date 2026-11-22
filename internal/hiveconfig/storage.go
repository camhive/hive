package hiveconfig

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ErrStorageSettings means SaveStorageSettings refused the settings as
// hive.yaml would.
var ErrStorageSettings = errors.New("hiveconfig: invalid storage settings")

// StorageSettings are the hive-wide keeping limits the app may change
// (#150, ADR-0024 §3); the running retention sweeper takes them at once.
type StorageSettings struct {
	RetentionDays int `json:"retention_days"`
	MinFreeGB     int `json:"min_free_gb"`
}

// StorageSettings returns the keeping limits of this configuration.
func (c Config) StorageSettings() StorageSettings {
	return StorageSettings{RetentionDays: c.RetentionDays, MinFreeGB: c.MinFreeGB}
}

// SaveStorageSettings validates s like hive.yaml and writes it into
// hive.yaml in place, as SaveUpdateSettings does (and one at a time with
// it, editMu).
func SaveStorageSettings(p Paths, s StorageSettings) error {
	editMu.Lock()
	defer editMu.Unlock()
	cfg, _, err := Load(p)
	if err != nil {
		return err
	}
	cfg.RetentionDays, cfg.MinFreeGB = s.RetentionDays, s.MinFreeGB
	if err := cfg.validateStorage(); err != nil {
		return fmt.Errorf("%w: %w", ErrStorageSettings, err)
	}
	raw, err := os.ReadFile(p.Config) //nolint:gosec // path from our own Paths
	if err != nil {
		return fmt.Errorf("hiveconfig: read %s: %w", p.Config, err)
	}
	out, err := setKeys(raw, []entry{{"retention_days", strconv.Itoa(s.RetentionDays), "!!int"}, {"min_free_gb", strconv.Itoa(s.MinFreeGB), "!!int"}})
	if err != nil {
		return fmt.Errorf("hiveconfig: edit %s: %w", p.Config, err)
	}
	return replaceFile(p.Config, out)
}

// SaveArchiveDir writes archive_dir into hive.yaml in place (#155: hive
// move-data points the copied file at the archive on the new disk).
func SaveArchiveDir(p Paths, dir string) error {
	editMu.Lock()
	defer editMu.Unlock()
	raw, err := os.ReadFile(p.Config) //nolint:gosec // path from our own Paths
	if err != nil {
		return fmt.Errorf("hiveconfig: read %s: %w", p.Config, err)
	}
	out, err := setKeys(raw, []entry{{"archive_dir", dir, "!!str"}})
	if err != nil {
		return fmt.Errorf("hiveconfig: edit %s: %w", p.Config, err)
	}
	return replaceFile(p.Config, out)
}
