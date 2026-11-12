//go:build !windows

package update

import (
	"os"
	"syscall"
)

func interrupt(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
