//go:build !windows

package update

import (
	"fmt"
	"os"
	"syscall"
)

// EnsurePrivate creates dir for the updater alone (0700) or makes it so:
// it must be a real directory owned by this process's user. Anything else
// is ErrUnsafeDir, and the updater does not install from or run out of it.
func EnsurePrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is not a plain directory", ErrUnsafeDir, dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("%w: %s belongs to uid %d", ErrUnsafeDir, dir, sys.Uid)
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700) //nolint:gosec // a directory: owner may list and enter it
	}
	return nil
}
