//go:build unix

package diskhealth

import (
	"os"
	"syscall"
)

// StatDev is the device number of the file system holding path.
func StatDev(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return uint64(st.Dev), nil //nolint:unconvert // int32 on some systems
}
