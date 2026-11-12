package update

import "os"

// interrupt on Windows: a console child cannot be signalled reliably from a
// service; the Hive's archive is written in fragments and survives a kill.
func interrupt(p *os.Process) error { return p.Kill() }
