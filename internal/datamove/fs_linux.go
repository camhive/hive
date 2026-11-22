//go:build linux

package datamove

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// fsNames are the file systems check names by their statfs magic.
var fsNames = map[int64]string{0x4d44: "vfat", 0x2011bab0: "exfat", 0x5346544e: "ntfs", 0x7366746e: "ntfs", 0x65735546: "fuseblk"} // ntfs: the old kernel driver and ntfs3 (Linux 5.15+); ntfs-3g and exfat-fuse show as fuseblk

// fsType names dir's file system when it is one check cares about.
func fsType(dir string) (string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return "", err
	}
	return fsNames[int64(st.Type)], nil //nolint:unconvert // int32 on some architectures
}

// deviceOf is the device number of dir's file system.
func deviceOf(dir string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:unconvert // uint32 on some architectures
}

// dropCache asks the kernel to forget f's cached pages, so the read-back
// that verifies a copy comes from the disk.
func dropCache(f *os.File) { _ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED) } //nolint:gosec // a descriptor fits int

// lock keeps a second move-data off dir while this one runs.
func lock(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, File+".lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // the data directory
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil { //nolint:gosec // a descriptor fits int
		_ = f.Close()
		return nil, errLocked
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil //nolint:gosec // a descriptor fits int
}
