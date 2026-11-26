package diskhealth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Device is the block device the archive is on.
type Device struct {
	Mount  string   // mount point holding the archive
	Source string   // what was mounted (/dev/sdb1, /dev/mapper/vg-root)
	Names  []string // kernel names the kernel log may use: the device, its slaves, their disks
	Disks  []string // whole disks to ask SMART about (/dev/sdb)
}

// mountOf finds the mount holding path in /proc/self/mountinfo's text:
// the longest mount point that is path or a parent of it. It returns the
// mount point, the device number (major:minor) and the source.
func mountOf(mountinfo, path string) (point, devno, source string, err error) {
	path = filepath.Clean(path)
	for line := range strings.Lines(mountinfo) {
		f := strings.Fields(line)
		sep := indexOf(f, "-")
		if len(f) < 5 || sep < 0 || sep+2 >= len(f) {
			continue
		}
		mp := unescape(f[4])
		if !within(path, mp) || len(mp) < len(point) {
			continue
		}
		point, devno, source = mp, f[2], f[sep+2]
	}
	if point == "" {
		return "", "", "", errors.New("diskhealth: no mount holds " + path)
	}
	return point, devno, source, nil
}

func indexOf(f []string, s string) int {
	for i, x := range f {
		if x == s {
			return i
		}
	}
	return -1
}

// within reports whether path is mp or below it.
func within(path, mp string) bool {
	return path == mp || mp == "/" || strings.HasPrefix(path, mp+"/")
}

// unescape undoes mountinfo's octal escapes (\040 for a space).
func unescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// resolve follows a device number through sysfs (/sys/dev/block/<devno>)
// to the kernel names of the device, of what it is built on (LVM, dm-crypt:
// its slaves) and of their whole disks.
func resolve(sysDevBlock, devno string) (names, disks []string) {
	dir, err := filepath.EvalSymlinks(filepath.Join(sysDevBlock, devno))
	if err != nil {
		return nil, nil
	}
	w := walker{seen: map[string]bool{}}
	w.walk(dir, 0)
	return w.names, w.disks
}

// walker collects names and disks down a device's slaves.
type walker struct {
	seen         map[string]bool
	names, disks []string
}

func (w *walker) name(n string) bool {
	if w.seen[n] {
		return false
	}
	w.seen[n] = true
	w.names = append(w.names, n)
	return true
}

func (w *walker) walk(dir string, depth int) {
	if depth > 8 || !w.name(filepath.Base(dir)) {
		return
	}
	slaves, _ := os.ReadDir(filepath.Join(dir, "slaves"))
	for _, s := range slaves {
		if d, err := filepath.EvalSymlinks(filepath.Join(dir, "slaves", s.Name())); err == nil {
			w.walk(d, depth+1)
		}
	}
	if len(slaves) > 0 {
		return
	}
	disk := filepath.Base(dir)
	if _, err := os.Stat(filepath.Join(dir, "partition")); err == nil {
		disk = filepath.Base(filepath.Dir(dir))
		w.name(disk)
	}
	w.disks = append(w.disks, "/dev/"+disk)
}

// findDevice is the archive's device on Linux.
func findDevice(mountinfoPath, sysDevBlock, archive string) (Device, error) {
	raw, err := os.ReadFile(mountinfoPath) //nolint:gosec // the kernel's file, or a test's
	if err != nil {
		return Device{}, err
	}
	abs, err := filepath.Abs(archive)
	if err != nil {
		return Device{}, err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	point, devno, source, err := mountOf(string(raw), abs)
	if err != nil {
		return Device{}, err
	}
	names, disks := resolve(sysDevBlock, devno)
	return Device{Mount: point, Source: source, Names: names, Disks: disks}, nil
}

// IsMountPoint reports whether mp is a mount point in the mountinfo file
// at path (/proc/self/mountinfo): a bind mount counts, whatever device it
// shares with its parent (#172 review 2).
func IsMountPoint(path, mp string) (bool, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the kernel's file, or a test's
	if err != nil {
		return false, err
	}
	mp = filepath.Clean(mp)
	if real, err := filepath.EvalSymlinks(mp); err == nil {
		mp = real // the kernel lists the resolved path (#172 review 3)
	}
	for line := range strings.Lines(string(raw)) {
		if f := strings.Fields(line); len(f) > 4 && filepath.Clean(unescape(f[4])) == mp {
			return true, nil
		}
	}
	return false, nil
}
