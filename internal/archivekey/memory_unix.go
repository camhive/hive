//go:build linux || darwin || freebsd || openbsd || netbsd

package archivekey

import "golang.org/x/sys/unix"

// lockMemory keeps b's pages out of swap; without the privilege or under
// RLIMIT_MEMLOCK it fails, and the key is kept unlocked rather than not at all.
func lockMemory(b []byte) { _ = unix.Mlock(b) }

func unlockMemory(b []byte) { _ = unix.Munlock(b) }

// DisableCoreDumps keeps the hive's memory, and the archive key in it, out
// of core dumps: no core file size, and on Linux not dumpable at all (which
// also keeps other processes of the same user from attaching to it).
func DisableCoreDumps() error {
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return err
	}
	return notDumpable()
}
