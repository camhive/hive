//go:build !windows

package retention

import "golang.org/x/sys/unix"

// Free reports bytes available to an unprivileged process on dir's volume.
func (osFS) Free(dir string) (int64, error) {
	d, err := DiskOf(dir)
	return d.Free, err
}

// DiskOf reports the size of dir's volume and the bytes available on it
// to an unprivileged process (Bavail, what the space rule measures).
func DiskOf(dir string) (Disk, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Disk{}, err
	}
	return Disk{Total: int64(st.Blocks) * int64(st.Bsize), Free: int64(st.Bavail) * int64(st.Bsize)}, nil //nolint:gosec // sizes fit int64 on supported platforms
}
