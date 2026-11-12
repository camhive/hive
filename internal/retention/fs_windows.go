//go:build windows

package retention

import "golang.org/x/sys/windows"

// Free reports bytes available to the caller on dir's volume.
func (osFS) Free(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return int64(free), nil //nolint:gosec // fits int64
}
