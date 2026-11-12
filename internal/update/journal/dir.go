package journal

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDir is hive-updater's own directory (ADR-0018, security review):
// versions, trusted metadata and this journal live there, never in the
// Hive's data directory, which may be on removable media or writable by
// other users. CAMHIVE_UPDATER_DIR overrides it (tests, unusual systems);
// it is set only by whoever installs the service.
func DefaultDir() string {
	if d := os.Getenv("CAMHIVE_UPDATER_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramFiles")
		if base == "" {
			base = `C:\Program Files`
		}
		return filepath.Join(base, "Camhive", "updater")
	}
	return "/var/lib/camhive-updater"
}
