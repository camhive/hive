package journal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// requestFile asks hive-updater to install what is offered now, whatever
// the window or manual mode says. `hive update` and the app's "Update now"
// (#82, through the hive) write it; the updater takes it within 30 s. It
// lives in the updater's private directory: only root can ask.
const requestFile = "update-request"

// RequestUpdate asks the updater in dir to update now.
func RequestUpdate(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return err // no updater on this machine
	}
	return os.WriteFile(filepath.Join(dir, requestFile), nil, 0o600)
}

// TakeRequest reports whether an update was asked for and clears it.
func TakeRequest(dir string) (bool, error) {
	err := os.Remove(filepath.Join(dir, requestFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// updaterFile names the running hive-updater, written at its start. An
// updater older than v0.1.7 does not write it: it reads hive.yaml only at start.
const updaterFile = "updater.json"

// Announce records that an updater of this version runs and reads its
// settings at every check.
func Announce(dir, version string) error {
	raw, _ := json.Marshal(map[string]string{"version": version})
	return os.WriteFile(filepath.Join(dir, updaterFile), raw, 0o600)
}

// Announced is the version of the updater that announced itself, or ""
// when none did.
func Announced(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, updaterFile)) //nolint:gosec // the updater's own directory
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var u struct{ Version string }
	return u.Version, json.Unmarshal(raw, &u)
}
