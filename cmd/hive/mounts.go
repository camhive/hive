package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/camhive/hive/internal/diskhealth"
	"github.com/camhive/hive/internal/hiveconfig"
)

// The service waits for its disk (#172, ADR-0024 amendment): a data
// directory on another file system than / is named in RequiresMountsFor=,
// and once it holds the hive's identity in ConditionPathExists=, so
// systemd never starts the hive beside a missing disk.

// otherFS reports whether dir is on another file system than /, and
// systemdHere whether the service is a systemd unit (other inits read
// Dependencies differently); tests replace them.
var (
	otherFS     = onOtherFilesystem
	systemdHere = hasSystemd
)

// onOtherFilesystem compares the device of dir, or of its nearest
// ancestor that exists, with the device of /. False where device numbers
// cannot be read (Windows).
func onOtherFilesystem(dir string) bool {
	root, err := diskhealth.StatDev("/")
	if err != nil {
		return false
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if dev, err := diskhealth.StatDev(d); err == nil {
			return dev != root
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}

// hasKey reports whether dir holds a hive identity.
func hasKey(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "hive.key")) //nolint:gosec // CAMHIVE_DATA_DIR, set by root for its own service
	return err == nil
}

// unitMounts is what the service waits for: the directories on other
// disks than / (the data directory, and archive_dir when it lies outside
// it), and the identity the data directory must hold once it has one (a
// first install creates it at its first start).
type unitMounts struct {
	dirs []string
	key  string
}

// mountsFor judges the data and archive directories with other.
func mountsFor(data, archive string, key bool, other func(string) bool) unitMounts {
	var m unitMounts
	if data != "" && other(data) {
		m.dirs = append(m.dirs, data)
		if key {
			m.key = filepath.Join(data, "hive.key")
		}
	}
	covered := len(m.dirs) > 0 && within(archive, data) // the data directory's line holds it
	if archive != "" && !covered && other(archive) {
		m.dirs = append(m.dirs, archive)
	}
	return m
}

// lines are the [Unit] lines.
func (m unitMounts) lines() []string {
	var out []string
	for _, d := range m.dirs {
		out = append(out, "RequiresMountsFor="+strconv.Quote(d))
	}
	if m.key != "" {
		out = append(out, "ConditionPathExists="+m.key)
	}
	return out
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// The hive owns one drop-in for these lines, mountsDropIn beside the
// unit, and nothing else writes them (#172 review 2): hive install, hive
// move-data and every start of the installed service rewrite it from the
// current data directory and archive_dir, so no line for a directory the
// hive left behind survives (a stale ConditionPathExists would keep
// systemd from starting the hive once the old disk is gone).
const mountsDropIn = "mounts.conf"

// mountsConf is the drop-in's content for data and archive: their lines
// only. A directory that looks on the system disk now keeps the lines
// prev gave it, since that is how a missing disk looks. "" for none.
func mountsConf(prev, data, archive string, key bool, other func(string) bool) string {
	had := map[string]bool{}
	for l := range strings.Lines(prev) {
		had[strings.TrimSpace(l)] = true
	}
	keep := func(d string) bool { return other(d) || had["RequiresMountsFor="+strconv.Quote(d)] }
	key = key || had["ConditionPathExists="+filepath.Join(data, "hive.key")]
	lines := mountsFor(data, archive, key, keep).lines()
	if len(lines) == 0 {
		return ""
	}
	return "# Written by the hive (#172): wait for the disks it records to.\n[Unit]\n" + strings.Join(lines, "\n") + "\n"
}

// writeMounts makes the drop-in at path hold content (none when empty) and
// reports whether it changed.
func writeMounts(path, content string) (bool, error) {
	prev, err := os.ReadFile(path) //nolint:gosec // the hive's own drop-in
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	switch {
	case content == "" && !exists, exists && string(prev) == content:
		return false, nil
	case content == "":
		return true, os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // systemd reads drop-ins as root
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil { //nolint:gosec // a unit file, world-readable like the unit
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// ensureMountLines rewrites the hive's drop-in beside unit for the data
// and archive directories and reloads systemd when it changed; the running
// hive is not restarted, the next start uses it.
func ensureMountLines(unit string, systemctl func(...string) error, data, archive string) (bool, error) {
	path := filepath.Join(unit+".d", mountsDropIn)
	prev, _ := os.ReadFile(path) //nolint:gosec // the hive's own drop-in
	changed, err := writeMounts(path, mountsConf(string(prev), data, archive, hasKey(data), otherFS))
	if err != nil || !changed {
		return changed, err
	}
	return true, systemctl("daemon-reload")
}

// installMounts is ensureMountLines for `hive install`: the data
// directory and hive.yaml's archive_dir.
func installMounts(unit string, systemctl func(...string) error, p hiveconfig.Paths) error {
	archive := ""
	if cfg, err := hiveconfig.Read(p); err == nil {
		archive = cfg.ArchiveDir
	}
	_, err := ensureMountLines(unit, systemctl, p.Data, archive)
	return err
}

// errDiskMissing: the service expects the data directory on a disk that is
// not mounted.
var errDiskMissing = errors.New("the data directory's disk is not mounted")

// identityGuard refuses a start that would make a new, unclaimed hive
// beside a missing disk: CAMHIVE_DATA_DIR holds no identity, the service
// definition names a mount for it, and the directory is on the system
// disk. A first install on a mounted disk passes.
func identityGuard(dataDir, unit string, otherFS func(string) bool) error {
	if dataDir == "" || hasKey(dataDir) || !unitNamesMount(unit, dataDir) || otherFS(dataDir) {
		return nil
	}
	return fmt.Errorf("%w: %s has no hive.key and is on the system disk although the service expects a mount there; mount the disk (see docs/user/install-hive.md) and start the hive again", errDiskMissing, dataDir)
}

// unitNamesMount reports whether the unit or its drop-ins name dir in
// RequiresMountsFor= or ConditionPathExists=.
func unitNamesMount(unit, dir string) bool {
	files, _ := filepath.Glob(filepath.Join(unit+".d", "*.conf"))
	sort.Strings(files)
	for _, f := range append([]string{unit}, files...) {
		if fileNamesMount(f, filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

func fileNamesMount(path, dir string) bool {
	f, err := os.Open(path) //nolint:gosec // the service definition
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "RequiresMountsFor="); ok && unquote(v) == dir {
			return true
		}
		if v, ok := strings.CutPrefix(line, "ConditionPathExists="); ok && v == filepath.Join(dir, "hive.key") {
			return true
		}
	}
	return false
}

func unquote(v string) string {
	if u, err := strconv.Unquote(v); err == nil {
		return filepath.Clean(u)
	}
	return filepath.Clean(v)
}

// fstabMountFor is the mount point /etc/fstab configures for dir: the
// longest one that is dir or holds it, except / (#172 review: an empty
// mount point on the system disk is never the archive).
func fstabMountFor(fstab, dir string) string {
	dir = path.Clean(filepath.ToSlash(dir)) // fstab is Unix's: slashes
	best := ""
	for line := range strings.Lines(fstab) {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		mp := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`).Replace(f[1])
		if !strings.HasPrefix(mp, "/") || mp == "/" || len(mp) <= len(best) {
			continue
		}
		if mp = path.Clean(mp); dir == mp || strings.HasPrefix(dir, mp+"/") {
			best = mp
		}
	}
	return best
}

// archiveMountPoint is fstabMountFor from the system's /etc/fstab; "" where
// there is none (Windows).
func archiveMountPoint(dir string) string {
	raw, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return ""
	}
	return fstabMountFor(string(raw), dir)
}

// waitForDisksNextTime adds the mount lines the installed service lacks
// (#172 review): OTA updates never touch the unit, so every hive
// installed before 0.1.11 gets them at its first start under systemd.
func (p *program) waitForDisksNextTime(cfg hiveconfig.Config, log *slog.Logger) {
	if !asInstalledService(systemdUnit, p.paths.Data) {
		return
	}
	wrote, err := ensureMountLines(systemdUnit, runSystemctl, p.paths.Data, cfg.ArchiveDir)
	switch {
	case err != nil:
		log.Warn("the service could not be told to wait for its disks", "err", err)
	case wrote:
		log.Info("the service now waits for its disks at its next start", "drop_in", filepath.Join(systemdUnit+".d", mountsDropIn))
	}
}

// asInstalledService reports whether this hive runs as the installed
// systemd unit, also as hive-updater's child (#172 review 2): kardianos'
// Interactive() is true for any parent but systemd, so it cannot tell.
// systemd sets INVOCATION_ID for the unit's processes and the updater
// passes its environment on; but every process of a user unit inherits
// one too, terminals included (review 3), so the hive must also run from
// the data directory the installed unit names, or from the default one
// when it names none.
func asInstalledService(unit, dataDir string) bool {
	if !systemdHere() || os.Getenv("INVOCATION_ID") == "" {
		return false
	}
	if _, err := os.Stat(unit); err != nil { //nolint:gosec // the unit's fixed path, or a test's
		return false
	}
	want := unitDataDir(unit)
	if want == "" {
		want = hiveconfig.DefaultPaths().Data
	}
	return filepath.Clean(want) == filepath.Clean(dataDir)
}
