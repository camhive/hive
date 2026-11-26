package diskhealth_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/camhive/hive/internal/diskhealth"
)

// #172: the words SQLite and a stale mount use for a disk that went away.
func TestClassifyKnowsAStaleMountAndSQLitesCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want diskhealth.Class
	}{
		{&os.PathError{Op: "open", Path: "x", Err: syscall.ESTALE}, diskhealth.IO},
		{errors.New("store: upsert segment: disk I/O error (778)"), diskhealth.IO},
		{errors.New("database disk image is malformed (11)"), diskhealth.IO},
		{errors.New("file is not a database (26)"), diskhealth.IO},
		{errors.New("attempt to write a readonly database (8)"), diskhealth.ReadOnly},
		{&os.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}, diskhealth.None}, // alone it says nothing: the monitor knows whether the root existed
		{errors.New("no such table: camera"), diskhealth.None},
	} {
		if got := diskhealth.Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func healthyRunner() *runner {
	return &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/sdb": "smart-ata-ok.json", "journalctl": "journal-empty.txt"}}
}

// #172: the archive directory the monitor saw is gone (its mount
// vanished): that is a failed disk, not "no archive yet".
func TestAVanishedArchiveIsFailed(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	failing, failed := &episode{}, &episode{}
	m := h.monitor(c, healthyRunner(), failing, failed)
	if rep := m.Check(context.Background()); rep.State != diskhealth.OK {
		t.Fatalf("first check = %+v, want ok", rep)
	}
	if err := os.RemoveAll(h.archive); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(30 * time.Minute)
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failed || !hasSignal(rep, diskhealth.SignalDevice) || failed.begun != 1 {
		t.Fatalf("report = %+v, failed begun %d; want failed on device evidence", rep, failed.begun)
	}
}

// A hive whose archive never existed yet (nothing recorded) is not failed.
func TestAnArchiveThatNeverExistedIsNotFailed(t *testing.T) {
	h := newHost(t)
	if err := os.RemoveAll(h.archive); err != nil {
		t.Fatal(err)
	}
	rep := h.monitor(&clock{t0}, healthyRunner(), &episode{}, &episode{}).Check(context.Background())
	if rep.State == diskhealth.Failed || hasSignal(rep, diskhealth.SignalDevice) {
		t.Fatalf("report = %+v, want no device evidence before the archive was ever seen", rep)
	}
}

// #172: the archive root on another device than at the first check (the
// disk remounted, or the empty mount point on the system disk).
func TestAnArchiveOnAnotherDeviceIsFailed(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	dev := uint64(0x811)
	failed := &episode{}
	m := h.monitor(c, healthyRunner(), &episode{}, failed, func(o *diskhealth.Options) {
		o.StatDev = func(string) (uint64, error) { return dev, nil }
	})
	m.Check(context.Background())
	dev = 0xb302
	c.t = c.t.Add(30 * time.Minute)
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failed || !hasSignal(rep, diskhealth.SignalDevice) || failed.begun != 1 {
		t.Fatalf("report = %+v, want failed on a device change", rep)
	}
	dev = 0x811 // the same file system again
	c.t = c.t.Add(30 * time.Minute)
	if rep := m.Check(context.Background()); rep.State == diskhealth.Failed {
		t.Fatalf("report = %+v, want no longer failed on the original device", rep)
	}
}

// #172: the device lookup working once and failing later is evidence.
func TestTheDeviceLookupFailingAfterItWorkedIsEvidence(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	m := h.monitor(c, healthyRunner(), &episode{}, &episode{})
	m.Check(context.Background())
	if err := os.Remove(h.mountinfo); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(30 * time.Minute)
	if rep := m.Check(context.Background()); !hasSignal(rep, diskhealth.SignalDevice) {
		t.Fatalf("report = %+v, want device evidence", rep)
	}
}

// #172 comment: one stale kernel line keeps disk.failing up for a day
// after a clean remount. An episode that rested only on kernel lines ends
// at the first check with healthy SMART, a working probe and no new lines.
func TestAKernelOnlyEpisodeEndsOnACleanCheck(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	r := &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/sdb": "smart-ata-ok.json", "journalctl": "journal-k.txt"}}
	failing := &episode{}
	m := h.monitor(c, r, failing, &episode{})
	if rep := m.Check(context.Background()); rep.State != diskhealth.Failing || failing.begun != 1 {
		t.Fatalf("first check = %+v, want failing on kernel lines", rep)
	}
	r.out["journalctl"] = "journal-empty.txt"
	c.t = c.t.Add(30 * time.Minute)
	m.Check(context.Background())
	if failing.ended != 1 {
		t.Fatalf("failing ended %d, want 1 at the first clean check", failing.ended)
	}
}

// Without SMART, or when the episode also had failed writes, the day of
// health still applies.
func TestAnEpisodeWithMoreThanKernelLinesWaitsADay(t *testing.T) {
	for name, tc := range map[string]struct {
		smart  bool
		writes bool
	}{"no SMART": {smart: false}, "kernel and writes": {smart: true, writes: true}} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			c := &clock{t0}
			r := &runner{out: map[string]string{"journalctl": "journal-k.txt"}}
			if tc.smart {
				r.out["smartctl -j -n standby -H -A /dev/sdb"] = "smart-ata-ok.json"
			}
			failing := &episode{}
			m := h.monitor(c, r, failing, &episode{})
			if tc.writes {
				m.RecordWrite(syscall.EIO)
			}
			m.Check(context.Background())
			r.out["journalctl"] = "journal-empty.txt"
			c.t = c.t.Add(30 * time.Minute)
			m.Check(context.Background())
			if failing.begun != 1 || failing.ended != 0 {
				t.Fatalf("failing begun %d ended %d, want begun and still on", failing.begun, failing.ended)
			}
		})
	}
}

func hasSignal(rep diskhealth.Report, s diskhealth.Signal) bool { //nolint:unparam // reads as a sentence at the call
	for _, e := range rep.Evidence {
		if e.Signal == s {
			return true
		}
	}
	return false
}

// #172 review 2: whether a mount point is mounted is read from
// mountinfo, so a bind mount of a directory on the same file system (same
// device as its parent) counts as mounted.
func TestIsMountPointReadsMountinfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mountinfo")
	info := `22 1 179:2 / / rw,noatime shared:1 - ext4 /dev/root rw
30 22 8:17 / /mnt/cameras rw,relatime shared:9 - ext4 /dev/sdb1 rw
31 22 179:2 /srv/archive /mnt/bound rw,relatime shared:1 - ext4 /dev/root rw
32 22 8:33 / /mnt/big\040disk rw,relatime shared:10 - ext4 /dev/sdc1 rw
`
	if err := os.WriteFile(path, []byte(info), 0o600); err != nil {
		t.Fatal(err)
	}
	for mp, want := range map[string]bool{"/mnt/cameras": true, "/mnt/bound": true, "/mnt/big disk": true, "/mnt/cameras/": true, "/mnt/usb": false, "/mnt": false} {
		got, err := diskhealth.IsMountPoint(path, mp)
		if err != nil || got != want {
			t.Errorf("IsMountPoint(%q) = %v, %v; want %v", mp, got, err, want)
		}
	}
	if _, err := diskhealth.IsMountPoint(filepath.Join(t.TempDir(), "none"), "/mnt"); err == nil {
		t.Error("no error without mountinfo")
	}
}

// #172 review 3: the kernel lists the resolved path; an fstab mount point
// reached through a symlink is mounted all the same.
func TestIsMountPointResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	link := filepath.Join(dir, "cameras")
	if err := os.MkdirAll(real, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(dir, "mountinfo")
	if err := os.WriteFile(info, []byte("30 22 8:17 / "+resolved+" rw,relatime shared:9 - ext4 /dev/sdb1 rw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := diskhealth.IsMountPoint(info, link); err != nil || !ok {
		t.Fatalf("IsMountPoint(symlink) = %v, %v; want mounted", ok, err)
	}
}
