//go:build darwin || freebsd || openbsd || netbsd

package archivekey

func notDumpable() error { return nil }
