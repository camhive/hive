package datamove

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// unsafeChars are what systemd splits, expands or escapes in the service
// definition (whitespace, % \ " ' $) and what a shell would act on in the
// command the owner copies into a root shell (; & | ( ) < > * ? # ` ! ~ { } [ ]).
const unsafeChars = `%\"'$;&|()<>*?#` + "`" + `!~{}[]`

// SafePath reports why a directory cannot be written into the service
// definition and used by the hive: it must be absolute and hold none of
// unsafeChars, whitespace or control characters. "" when it is fine. The
// app repeats the rule (web/app/src/lib/diskHealth.ts).
func SafePath(dir string) string {
	switch {
	case !filepath.IsAbs(dir):
		return "give the new directory as an absolute path, e.g. /mnt/newdisk/camhive"
	case strings.ContainsFunc(dir, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(unsafeChars, r) }):
		return "the directory must not contain spaces or any of " + unsafeChars + ": choose a plain path such as /mnt/newdisk/camhive"
	}
	return ""
}

// unusable are file systems that cannot hold the hive: no Unix permissions
// (hive.key and the keyring would be readable by every user) and, for FAT,
// files of at most 4 GiB.
var unusable = map[string]bool{"vfat": true, "exfat": true, "ntfs": true, "fuseblk": true}

// check refuses a target the hive cannot run from: an unsafe path, one
// inside or around the data or archive directory, one on the disk being
// replaced, or one on a file system without permissions.
func (o *Options) check() error {
	if why := SafePath(o.To); why != "" {
		return fmt.Errorf("datamove: %s: %s", o.To, why)
	}
	o.To = filepath.Clean(o.To)
	for _, dir := range []string{o.From.Data, o.ArchiveDir} {
		if inside(o.To, dir) || inside(dir, o.To) {
			return fmt.Errorf("datamove: %s overlaps %s: choose a directory on the new disk", o.To, dir)
		}
	}
	at := existing(o.To)
	if fs, err := o.FSType(at); err == nil && unusable[fs] {
		return fmt.Errorf("datamove: %s is on a %s file system, which cannot keep the hive's keys private (and FAT limits files to 4 GiB): format the new disk as ext4", o.To, fs)
	}
	return o.otherDisk(at)
}

// otherDisk refuses a target on the device of the data or the archive.
func (o *Options) otherDisk(at string) error {
	to, err := o.DeviceOf(at)
	if err != nil || to == 0 {
		return nil //nolint:nilerr // not known here: nothing to compare
	}
	for _, dir := range []string{o.From.Data, o.ArchiveDir} {
		if dev, err := o.DeviceOf(dir); err == nil && dev == to {
			return fmt.Errorf("datamove: %s is on the same disk as %s: mount the new disk and choose a directory on it", o.To, dir)
		}
	}
	return nil
}

// existing is dir or its nearest parent that exists.
func existing(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil || !errors.Is(err, os.ErrNotExist) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
