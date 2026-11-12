// Package version carries build metadata injected by the release pipeline
// (-ldflags "-X ...") and formats it for `--version` output and heartbeats.
package version

import "fmt"

// Info describes one build of a Camhive binary.
type Info struct {
	Version string // semantic version, e.g. "1.2.3"; empty for local builds
	Commit  string // short git commit
	Date    string // build date, YYYY-MM-DD
}

// Set by the build pipeline via -ldflags; read through Current().
var (
	version string
	commit  string
	date    string
)

// Current returns the build metadata of the running binary.
func Current() Info {
	return Info{Version: version, Commit: commit, Date: date}
}

// String renders the info as "1.2.3 (abc1234, 2026-11-08)". A build without a
// version is "dev". Commit and date appear only when present.
func (i Info) String() string {
	v := i.Version
	if v == "" {
		v = "dev"
	}
	switch {
	case i.Commit != "" && i.Date != "":
		return fmt.Sprintf("%s (%s, %s)", v, i.Commit, i.Date)
	case i.Commit != "":
		return fmt.Sprintf("%s (%s)", v, i.Commit)
	case i.Date != "":
		return fmt.Sprintf("%s (%s)", v, i.Date)
	default:
		return v
	}
}
