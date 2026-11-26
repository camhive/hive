package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
)

// #172: a data directory on another file system than / gets a mount
// dependency, and a condition on its identity once it has one; so does an
// archive_dir on another disk than the data directory (#172 review: the
// split layout), so systemd never starts the hive beside a missing disk.
func TestMountLinesForDirectoriesOnAnotherDisk(t *testing.T) {
	const data, archive = "/media/usb/camhive", "/mnt/cameras/archive"
	for _, tc := range []struct {
		name    string
		other   []string
		archive string
		key     bool
		want    []string
	}{
		{"all on the system disk", nil, archive, true, nil},
		{"data on another disk, identity there", []string{data}, "", true, []string{`RequiresMountsFor="/media/usb/camhive"`, "ConditionPathExists=/media/usb/camhive/hive.key"}},
		{"data on another disk, first install", []string{data}, "", false, []string{`RequiresMountsFor="/media/usb/camhive"`}},
		{"archive inside the data directory", []string{data, data + "/archive"}, data + "/archive", true, []string{`RequiresMountsFor="/media/usb/camhive"`, "ConditionPathExists=/media/usb/camhive/hive.key"}},
		{"split: data on the system disk, archive on another", []string{archive}, archive, true, []string{`RequiresMountsFor="/mnt/cameras/archive"`}},
		{"both on other disks", []string{data, archive}, archive, false, []string{`RequiresMountsFor="/media/usb/camhive"`, `RequiresMountsFor="/mnt/cameras/archive"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := func(d string) bool { return slices.Contains(tc.other, d) }
			got := mountsFor(data, tc.archive, tc.key, other).lines()
			if runtime.GOOS == "windows" {
				t.Skip("unix paths")
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
		})
	}
}

// `hive install` writes the hive's own drop-in: the data directory on
// another file system, and hive.yaml's archive_dir when that is on another
// disk (the split layout).
func TestInstallWritesTheMountsDropIn(t *testing.T) {
	unit := filepath.Join(t.TempDir(), "camhive-hive.service")
	data, archive := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "hive.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := hiveconfig.Paths{}.WithDataDir(data)
	reload := 0
	systemctl := func(...string) error { reload++; return nil }
	read := func() string { b, _ := os.ReadFile(filepath.Join(unit+".d", mountsDropIn)); return string(b) }

	defer stubOtherFS(func(d string) bool { return d == data })()
	if err := installMounts(unit, systemctl, paths); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, "RequiresMountsFor="+strconv.Quote(data)+"\n") || !strings.Contains(got, "ConditionPathExists="+filepath.Join(data, "hive.key")+"\n") || reload != 1 {
		t.Fatalf("drop-in =\n%s\nreloads %d; want the data directory's lines and a reload", got, reload)
	}

	// The split layout: data on the system disk, archive_dir on another.
	if err := os.WriteFile(paths.Config, []byte("archive_dir: "+archive+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer stubOtherFS(func(d string) bool { return d == archive })()
	if err := os.Remove(filepath.Join(unit+".d", mountsDropIn)); err != nil {
		t.Fatal(err)
	}
	if err := installMounts(unit, systemctl, paths); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, "RequiresMountsFor="+strconv.Quote(archive)+"\n") || strings.Contains(got, strconv.Quote(data)) {
		t.Fatalf("split layout drop-in =\n%s\nwant the archive's mount only", got)
	}
}

// #172 review 2: the hive owns its drop-in. It writes the lines for the
// current directories, keeps them while a disk is missing (the directory
// then looks on the system disk), drops every line for a directory it no
// longer uses, removes the file when nothing is left, and reloads only
// when the file changed.
func TestTheHiveOwnsItsMountsDropIn(t *testing.T) {
	unit := filepath.Join(t.TempDir(), "camhive-hive.service")
	path := filepath.Join(unit+".d", mountsDropIn)
	old, moved := t.TempDir(), t.TempDir()
	for _, d := range []string{old, moved} {
		if err := os.WriteFile(filepath.Join(d, "hive.key"), []byte("k"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reloads := 0
	systemctl := func(...string) error { reloads++; return nil }
	read := func() string { b, _ := os.ReadFile(path); return string(b) }

	defer stubOtherFS(func(d string) bool { return d == old })()
	if wrote, err := ensureMountLines(unit, systemctl, old, ""); err != nil || !wrote || !strings.Contains(read(), strconv.Quote(old)) {
		t.Fatalf("first start: wrote %v, %v, drop-in %q", wrote, err, read())
	}
	if wrote, _ := ensureMountLines(unit, systemctl, old, ""); wrote || reloads != 1 {
		t.Fatalf("same again: wrote %v, %d reloads; want nothing", wrote, reloads)
	}
	if !unitNamesMount(unit, old) {
		t.Fatal("the guard does not see the drop-in's mount")
	}

	_ = os.Remove(filepath.Join(old, "hive.key"))
	defer stubOtherFS(func(string) bool { return false })() // the disk is missing: the directory looks on the system disk
	if wrote, _ := ensureMountLines(unit, systemctl, old, ""); wrote || !strings.Contains(read(), "ConditionPathExists="+filepath.Join(old, "hive.key")) {
		t.Fatalf("disk missing: wrote %v, drop-in %q; want the lines kept", wrote, read())
	}

	defer stubOtherFS(func(d string) bool { return d == moved })() // the hive runs from another disk now
	if wrote, _ := ensureMountLines(unit, systemctl, moved, ""); !wrote || strings.Contains(read(), old) || !strings.Contains(read(), strconv.Quote(moved)) {
		t.Fatalf("moved: wrote %v, drop-in %q; want only the new directory", wrote, read())
	}

	sys := t.TempDir()
	defer stubOtherFS(func(string) bool { return false })()
	if wrote, _ := ensureMountLines(unit, systemctl, sys, ""); !wrote {
		t.Fatal("back on the system disk: nothing changed")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drop-in left for a hive on the system disk: %q", read())
	}
}

// #172 review 2: a move leaves no line for the old directory: the move's
// switch rewrites the hive's drop-in for the new one, a switch back to the
// installed definition removes it, and a failed switch puts the old one
// back.
func TestAMoveLeavesNoLineForTheOldDirectory(t *testing.T) {
	sd := &systemd{startOK: func(string) bool { return true }, up: true}
	s := newOSService(t, sd)
	s.otherFS = func(string) bool { return true }
	mounts := filepath.Join(filepath.Dir(sd.dropIn), mountsDropIn)
	old := "[Unit]\nRequiresMountsFor=\"/mnt/old/camhive\"\nConditionPathExists=/mnt/old/camhive/hive.key\n"
	_ = os.MkdirAll(filepath.Dir(mounts), 0o750)
	if err := os.WriteFile(mounts, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	to := t.TempDir()
	if err := os.WriteFile(filepath.Join(to, "hive.key"), []byte("k"), 0o600); err != nil { // the move copies the keys first
		t.Fatal(err)
	}
	if err := s.Switch(to); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(mounts)
	if strings.Contains(string(b), "/mnt/old") || !strings.Contains(string(b), "RequiresMountsFor="+strconv.Quote(to)) || !strings.Contains(string(b), "ConditionPathExists="+filepath.Join(to, "hive.key")) {
		t.Fatalf("mounts drop-in after the move =\n%s", b)
	}
	if b, _ := os.ReadFile(sd.dropIn); string(b) != dropInFor(to) {
		t.Fatalf("data-dir drop-in = %q", b)
	}

	if err := s.Switch(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mounts); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the mounts drop-in survived a switch back to the installed definition")
	}

	_ = os.WriteFile(mounts, []byte(old), 0o600)
	sd.startOK = func(dropIn string) bool { return !strings.Contains(dropIn, to) }
	if err := s.Switch(to); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(mounts); string(b) != old {
		t.Fatalf("mounts drop-in after a failed switch = %q, want the old one back", b)
	}
}

// #172: a hive whose service names a mount for its data directory, while
// that directory sits on the system disk without an identity (the disk
// is not mounted), refuses to start instead of becoming a new hive.
func TestTheHiveRefusesToStartBesideAMissingDisk(t *testing.T) {
	data := t.TempDir()
	unit := filepath.Join(t.TempDir(), "camhive-hive.service")
	write := func(body string) {
		if err := os.WriteFile(unit, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	withMount := "[Unit]\nRequiresMountsFor=" + strconv.Quote(data) + "\n[Service]\nEnvironment=CAMHIVE_DATA_DIR=" + data + "\n"
	onRoot := func(string) bool { return false }

	write(withMount)
	if err := identityGuard(data, unit, onRoot); !errors.Is(err, errDiskMissing) {
		t.Fatalf("guard = %v, want errDiskMissing", err)
	}
	if err := identityGuard(data, unit, func(string) bool { return true }); err != nil {
		t.Fatalf("guard on the mounted disk of a first install = %v, want nil", err)
	}
	if err := identityGuard("", unit, onRoot); err != nil {
		t.Fatalf("guard without CAMHIVE_DATA_DIR = %v", err)
	}
	write("[Service]\nEnvironment=CAMHIVE_DATA_DIR=" + data + "\n")
	if err := identityGuard(data, unit, onRoot); err != nil {
		t.Fatalf("guard without a mount in the unit = %v, want nil (a data directory on the system disk)", err)
	}
	write(withMount)
	if err := os.WriteFile(filepath.Join(data, "hive.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := identityGuard(data, unit, onRoot); err != nil {
		t.Fatalf("guard with the identity there = %v", err)
	}
}

func stubOtherFS(f func(string) bool) func() {
	old := otherFS
	otherFS = f
	return func() { otherFS = old }
}

// #172 review: the mount point fstab configures for a directory, the
// longest that holds it; "/" and swap are none.
func TestFstabMountFor(t *testing.T) {
	fstab := `# /etc/fstab
PARTUUID=abc-01  /boot/firmware  vfat  defaults  0  2
PARTUUID=abc-02  /               ext4  defaults,noatime  0  1
UUID=0a1b  /mnt/cameras  ext4  defaults,nofail  0  2
UUID=0c0d  /mnt/cameras/big\040disk  ext4  defaults,nofail  0  2
/swapfile none swap sw 0 0
`
	for dir, want := range map[string]string{
		"/mnt/cameras/camhive/archive":  "/mnt/cameras",
		"/mnt/cameras":                  "/mnt/cameras",
		"/mnt/cameras/big disk/archive": "/mnt/cameras/big disk",
		"/mnt/camerasX/archive":         "",
		"/var/lib/camhive/archive":      "",
	} {
		if got := fstabMountFor(fstab, dir); got != want {
			t.Errorf("fstabMountFor(%q) = %q, want %q", dir, got, want)
		}
	}
}

// #172 review 2: since v0.1.5 the unit runs hive-updater and the hive is
// its child, so kardianos' Interactive() is true and cannot tell whether
// the hive runs as the installed service. systemd's INVOCATION_ID, which
// the updater passes on, and the installed unit can.
func TestTheServiceGatePassesUnderTheUpdater(t *testing.T) {
	oldSystemd := systemdHere
	systemdHere = func() bool { return true }
	defer func() { systemdHere = oldSystemd }()
	unit := filepath.Join(t.TempDir(), "camhive-hive.service")

	t.Setenv("INVOCATION_ID", "")
	if asInstalledService(unit, hiveconfig.DefaultPaths().Data) {
		t.Fatal("passes without INVOCATION_ID (a shell)")
	}
	t.Setenv("INVOCATION_ID", "0f1e2d3c4b5a69788796a5b4c3d2e1f0")
	if asInstalledService(unit, hiveconfig.DefaultPaths().Data) {
		t.Fatal("passes without an installed unit")
	}
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/usr/bin/hive-updater run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !asInstalledService(unit, hiveconfig.DefaultPaths().Data) {
		t.Fatal("fails for the hive started by hive-updater under systemd")
	}
	systemdHere = func() bool { return false }
	if asInstalledService(unit, hiveconfig.DefaultPaths().Data) {
		t.Fatal("passes without systemd")
	}
}

// #172 review 3: INVOCATION_ID is inherited by every process of a systemd
// user unit, terminals included, so a hive started by hand there looks
// like the service. Only a hive running from the data directory the
// installed unit names (or the default one when it names none) is it.
func TestAHandStartedHiveIsNotTheInstalledService(t *testing.T) {
	oldSystemd := systemdHere
	systemdHere = func() bool { return true }
	defer func() { systemdHere = oldSystemd }()
	t.Setenv("INVOCATION_ID", "0f1e2d3c4b5a69788796a5b4c3d2e1f0")
	unit := filepath.Join(t.TempDir(), "camhive-hive.service")
	write := func(body string) {
		if err := os.WriteFile(unit, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("[Service]\nExecStart=/usr/bin/hive-updater run\nEnvironment=\"CAMHIVE_DATA_DIR=/mnt/cameras/camhive\"\n")
	if asInstalledService(unit, "/tmp/x") {
		t.Fatal("a hand-started hive on /tmp/x passes as the service of /mnt/cameras/camhive")
	}
	if !asInstalledService(unit, "/mnt/cameras/camhive/") {
		t.Fatal("the service's own data directory fails")
	}

	write("[Service]\nExecStart=/usr/bin/hive-updater run\n") // installed without CAMHIVE_DATA_DIR
	if asInstalledService(unit, "/tmp/x") {
		t.Fatal("a hand-started hive on /tmp/x passes as the service of the default directory")
	}
	if !asInstalledService(unit, hiveconfig.DefaultPaths().Data) {
		t.Fatal("the default data directory fails for a unit without CAMHIVE_DATA_DIR")
	}
}
