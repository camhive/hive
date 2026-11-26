//go:build !unix

package diskhealth

import "os"

// StatDev is 0 where the device number cannot be read (Windows): only
// whether the path exists is checked.
func StatDev(path string) (uint64, error) {
	_, err := os.Stat(path)
	return 0, err
}
