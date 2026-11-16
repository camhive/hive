package archivekey

import "golang.org/x/sys/unix"

func notDumpable() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
