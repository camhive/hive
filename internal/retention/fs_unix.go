//go:build !windows

package retention

import "golang.org/x/sys/unix"

// Free reports bytes available to an unprivileged process on dir's volume.
func (osFS) Free(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:gosec // sizes fit int64 on supported platforms
}
