package update

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// EnsurePrivate creates dir and gives it a protected ACL: full control for
// SYSTEM and Administrators only, nothing inherited (C:\ProgramData lets
// users create files; Program Files does not, but the ACL is set anyway).
func EnsurePrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnsafeDir, dir, err)
	}
	return nil
}
