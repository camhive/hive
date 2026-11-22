//go:build windows

package retention

import "golang.org/x/sys/windows"

// Free reports bytes available to the caller on dir's volume.
func (osFS) Free(dir string) (int64, error) {
	d, err := DiskOf(dir)
	return d.Free, err
}

// DiskOf reports the size of dir's volume and the bytes available on it
// to the caller.
func DiskOf(dir string) (Disk, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Disk{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return Disk{}, err
	}
	return Disk{Total: int64(total), Free: int64(free)}, nil //nolint:gosec // fits int64
}
