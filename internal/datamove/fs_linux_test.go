//go:build linux

package datamove

import "testing"

// #155 re-review: every file system statfs can report for a FAT or NTFS
// disk is named and refused, the in-kernel ntfs3 driver (default since
// Linux 5.15) included.
func TestFileSystemsWithoutPermissionsAreKnownByTheirMagic(t *testing.T) {
	for magic, want := range map[int64]string{0x4d44: "vfat", 0x2011bab0: "exfat", 0x5346544e: "ntfs", 0x7366746e: "ntfs", 0x65735546: "fuseblk"} {
		if got := fsNames[magic]; got != want || !unusable[got] {
			t.Errorf("magic %#x = %q (refused %v), want %q refused", magic, got, unusable[got], want)
		}
	}
	for _, ok := range []int64{0xef53 /* ext4 */, 0x9123683e /* btrfs */, 0x58465342 /* xfs */} {
		if name := fsNames[ok]; unusable[name] {
			t.Errorf("magic %#x refused", ok)
		}
	}
}
