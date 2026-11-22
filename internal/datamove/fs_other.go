//go:build !linux

package datamove

import "os"

// hive move-data runs on Linux only (cmd/hive refuses elsewhere); these
// keep the package building for the other systems.

func fsType(string) (string, error)   { return "", nil }
func deviceOf(string) (uint64, error) { return 0, nil }
func dropCache(*os.File)              {}
func lock(string) (func(), error)     { return func() {}, nil }
