package archivekey

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockMemory keeps b's pages in RAM (VirtualLock); a failure leaves the key
// unlocked rather than not kept.
func lockMemory(b []byte) {
	_ = windows.VirtualLock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b))) //nolint:gosec // the address of our own slice
}

func unlockMemory(b []byte) {
	_ = windows.VirtualUnlock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b))) //nolint:gosec // as above
}

// DisableCoreDumps does nothing on Windows: the hive writes no dumps
// itself, and Windows Error Reporting's local dumps are an administrator's
// setting, which the user documentation names.
func DisableCoreDumps() error { return nil }
