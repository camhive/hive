package diskhealth

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// Class is the kind of a failed write.
type Class string

// Classes. A full disk is not a failing one: retention and storage.low
// deal with it.
const (
	None     Class = ""
	IO       Class = "eio"       // input/output error: the disk could not do it
	ReadOnly Class = "read_only" // the file system was remounted read-only, usually after I/O errors
	NoSpace  Class = "enospc"    // no space left
)

// disk reports whether the class says something about the disk's health.
func (c Class) disk() bool { return c == IO || c == ReadOnly }

// Classify names the class of a write error; None for anything else
// (permission, a missing directory).
func Classify(err error) Class {
	switch {
	case err == nil:
		return None
	case errors.Is(err, syscall.EIO):
		return IO
	case errors.Is(err, syscall.EROFS):
		return ReadOnly
	case errors.Is(err, syscall.ENOSPC):
		return NoSpace
	}
	return classifyText(err.Error())
}

// classifyText reads the class from an error's or a log line's words: the
// engine's log and SQLite's errors carry no errno.
func classifyText(s string) Class {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "input/output error"), strings.Contains(s, "disk i/o error"):
		return IO
	case strings.Contains(s, "read-only file system"):
		return ReadOnly
	case strings.Contains(s, "no space left on device"), strings.Contains(s, "database or disk is full"):
		return NoSpace
	}
	return None
}

// Writes counts failed writes since the last check by class.
type Writes struct {
	EIO, ReadOnly, NoSpace int
	First                  string // the first one that says something about the disk
}

func (w *Writes) add(c Class, what string) {
	switch c {
	case IO:
		w.EIO++
	case ReadOnly:
		w.ReadOnly++
	case NoSpace:
		w.NoSpace++
		return
	default:
		return
	}
	if w.First == "" {
		w.First = what
	}
}

func (w Writes) bad() bool { return w.EIO+w.ReadOnly > 0 }

func (w Writes) evidence() Evidence {
	return Evidence{Signal: SignalWrites, Detail: fmt.Sprintf("%d failed writes on the archive, first: %s", w.EIO+w.ReadOnly, w.First),
		Values: map[string]int64{"eio": int64(w.EIO), "read_only": int64(w.ReadOnly), "enospc": int64(w.NoSpace)}}
}
