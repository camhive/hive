package diskhealth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/camhive/hive/internal/diskhealth"
)

var t0 = time.Date(2026, 11, 21, 22, 30, 0, 0, time.UTC)

// clock is a settable Now.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// episode records what the monitor began and ended.
type episode struct {
	begun, ended int
	data         any
}

func (e *episode) Begin(_ context.Context, _ time.Time, data any) { e.begun++; e.data = data }
func (e *episode) End(context.Context, time.Time)                 { e.ended++ }

// runner answers commands from fixtures; an unknown command fails.
type runner struct {
	out   map[string]string // "smartctl -j -n standby -H -A /dev/sdb" -> fixture file
	calls []string
}

func (r *runner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	name = filepath.Base(name)
	cmd := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, cmd)
	for prefix, file := range r.out {
		if strings.HasPrefix(cmd, prefix) {
			b, err := os.ReadFile(filepath.Join("testdata", file))
			if err != nil {
				return nil, err
			}
			if name == "smartctl" {
				return b, exitError{} // smartctl exits non-zero for warnings; the output still counts
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s: not found", name)
}

type exitError struct{}

func (exitError) Error() string { return "exit status 64" }

// fakeHost is a Linux host whose archive lives on sdb1 (8:17).
type fakeHost struct {
	archive, mountinfo, sys string
}

func newHost(t *testing.T) fakeHost {
	t.Helper()
	root := t.TempDir()
	h := fakeHost{archive: filepath.Join(root, "disk", "archive"), mountinfo: filepath.Join(root, "mountinfo"), sys: filepath.Join(root, "sys")}
	if err := os.MkdirAll(h.archive, 0o750); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "mountinfo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mounts := strings.ReplaceAll(string(fixture), "/mnt/cameras ", filepath.Join(root, "disk")+" ")
	if err := os.WriteFile(h.mountinfo, []byte(mounts), 0o600); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(h.sys, "devices", "pci0000:00", "usb2", "host2", "block", "sdb", "sdb1")
	if err := os.MkdirAll(part, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(part, "partition"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.sys, "dev", "block"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(part, filepath.Join(h.sys, "dev", "block", "8:17")); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h fakeHost) monitor(c *clock, r *runner, failing, failed *episode, opts ...func(*diskhealth.Options)) *diskhealth.Monitor {
	o := diskhealth.Options{
		ArchiveDir: h.archive, GOOS: "linux", Now: c.now, Run: r.run,
		LookPath:  func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Mountinfo: h.mountinfo, SysDevBlock: filepath.Join(h.sys, "dev", "block"),
		Failing: failing, Failed: failed,
	}
	for _, f := range opts {
		f(&o)
	}
	return diskhealth.New(o)
}

func TestAHealthyDiskIsOKAndRaisesNothing(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	r := &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/sdb": "smart-ata-ok.json", "journalctl": "journal-empty.txt"}}
	failing, failed := &episode{}, &episode{}
	rep := h.monitor(c, r, failing, failed).Check(context.Background())
	if rep.State != diskhealth.OK || len(rep.Evidence) != 0 {
		t.Fatalf("report = %+v, want ok without evidence", rep)
	}
	if rep.Device != "/dev/sdb" || rep.TemperatureC == nil || *rep.TemperatureC != 34 || !rep.CheckedAt.Equal(t0) {
		t.Fatalf("report = %+v, want /dev/sdb at 34 °C checked at t0", rep)
	}
	if failing.begun+failed.begun != 0 {
		t.Fatalf("episodes begun: failing %d failed %d", failing.begun, failed.begun)
	}
}

func TestPendingSectorsBehindAUSBBridgeMakeTheDiskFailing(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	r := &runner{out: map[string]string{
		"smartctl -j -n standby -H -A -d sat /dev/sdb": "smart-ata-pending.json",
		"smartctl -j -n standby -H -A /dev/sdb":        "smart-usb-unknown.json",
	}}
	failing, failed := &episode{}, &episode{}
	rep := h.monitor(c, r, failing, failed).Check(context.Background())
	if rep.State != diskhealth.Failing {
		t.Fatalf("state = %s, want failing; %+v", rep.State, rep)
	}
	ev := rep.Evidence[0]
	if ev.Signal != diskhealth.SignalSMART || ev.Values["pending_sectors"] != 8 || ev.Values["reallocated_sectors"] != 120 || ev.Values["offline_uncorrectable"] != 2 {
		t.Fatalf("evidence = %+v, want SMART with pending 8, reallocated 120, uncorrectable 2", ev)
	}
	if failing.begun != 1 || failed.begun != 0 {
		t.Fatalf("failing begun %d, failed begun %d; want 1, 0", failing.begun, failed.begun)
	}
}

func TestSMARTFailedAndWornNVMeAreEvidence(t *testing.T) {
	for _, tc := range []struct {
		fixture, value string
		want           int64
	}{
		{"smart-ata-failed.json", "passed", 0},
		{"smart-nvme-worn.json", "percentage_used", 103},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			h := newHost(t)
			r := &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/sdb": tc.fixture}}
			rep := h.monitor(&clock{t0}, r, &episode{}, &episode{}).Check(context.Background())
			if rep.State != diskhealth.Failing || len(rep.Evidence) != 1 {
				t.Fatalf("report = %+v, want failing on SMART", rep)
			}
			if v, ok := rep.Evidence[0].Values[tc.value]; !ok || v != tc.want {
				t.Fatalf("values = %v, want %s = %d", rep.Evidence[0].Values, tc.value, tc.want)
			}
		})
	}
}

func TestKernelIOErrorsOnTheArchiveDeviceOnly(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	r := &runner{out: map[string]string{"journalctl -k": "journal-k.txt"}}
	m := h.monitor(c, r, &episode{}, &episode{}, func(o *diskhealth.Options) {
		o.LookPath = func(name string) (string, error) {
			if name == "smartctl" {
				return "", errors.New("not installed")
			}
			return "/usr/bin/" + name, nil
		}
	})
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failing || len(rep.Evidence) != 1 || rep.Evidence[0].Signal != diskhealth.SignalKernel {
		t.Fatalf("report = %+v, want failing on kernel errors", rep)
	}
	// sdb: two block errors, one buffer error, one EXT4-fs error; the sda line and the warning are not ours.
	if got := rep.Evidence[0].Values["io_errors"]; got != 4 {
		t.Fatalf("io_errors = %d, want 4", got)
	}
	if want := fmt.Sprintf("journalctl -k -o short-iso --no-pager -q --since @%d", t0.Add(-24*time.Hour).Unix()); !contains(r.calls, want) {
		t.Fatalf("calls = %v, want %q (the first check looks back a day)", r.calls, want)
	}
	c.t = c.t.Add(30 * time.Minute)
	m.Check(context.Background())
	if want := fmt.Sprintf("journalctl -k -o short-iso --no-pager -q --since @%d", t0.Unix()); !contains(r.calls, want) {
		t.Fatalf("calls = %v, want %q (later checks read from the last one)", r.calls, want)
	}
}

func TestWithoutDmesgOrJournalOrSMARTTheStateIsUnknown(t *testing.T) {
	h := newHost(t)
	rep := h.monitor(&clock{t0}, &runner{}, &episode{}, &episode{}, func(o *diskhealth.Options) {
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
	}).Check(context.Background())
	if rep.State != diskhealth.Unknown {
		t.Fatalf("state = %s, want unknown", rep.State)
	}
}

func TestOtherSystemsReportUnknown(t *testing.T) {
	h := newHost(t)
	r := &runner{out: map[string]string{"smartctl": "smart-ata-pending.json"}}
	rep := h.monitor(&clock{t0}, r, &episode{}, &episode{}, func(o *diskhealth.Options) { o.GOOS = "windows" }).Check(context.Background())
	if rep.State != diskhealth.Unknown || len(r.calls) != 0 {
		t.Fatalf("state = %s after %v, want unknown without running anything", rep.State, r.calls)
	}
}

func TestTheHivesOwnWriteErrorsCountEIOButNotAFullDisk(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	none := func(o *diskhealth.Options) {
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
	}
	failing, failed := &episode{}, &episode{}
	m := h.monitor(c, &runner{}, failing, failed, none)
	m.RecordWrite(fmt.Errorf("seal: %w", &os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}))
	if rep := m.Check(context.Background()); rep.State != diskhealth.Unknown {
		t.Fatalf("state after ENOSPC = %s, want unknown: a full disk is storage.low, not a failing one", rep.State)
	}
	c.t = c.t.Add(30 * time.Minute)
	m.RecordWrite(&os.PathError{Op: "write", Path: "x", Err: syscall.EIO})
	m.RecordWrite(errors.New("store: disk I/O error (10)")) // SQLite's words
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failing || rep.Evidence[0].Signal != diskhealth.SignalWrites || rep.Evidence[0].Values["eio"] != 2 {
		t.Fatalf("report = %+v, want failing on 2 EIO writes", rep)
	}
	if failing.begun != 1 {
		t.Fatalf("failing begun %d, want 1", failing.begun)
	}
}

func TestTheEnginesSegmentWriteErrorsAreReadFromItsLog(t *testing.T) {
	h := newHost(t)
	log := filepath.Join(t.TempDir(), "mediamtx.log")
	fixture, err := os.ReadFile(filepath.Join("testdata", "mediamtx.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	c := &clock{t0}
	m := h.monitor(c, &runner{}, &episode{}, &episode{}, func(o *diskhealth.Options) {
		o.EngineLog = log
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
	})
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failing || rep.Evidence[0].Values["eio"] != 1 || rep.Evidence[0].Values["read_only"] != 1 || rep.Evidence[0].Values["enospc"] != 1 {
		t.Fatalf("report = %+v, want writes evidence with eio 1, read_only 1, enospc 1", rep)
	}
	c.t = c.t.Add(30 * time.Minute)
	if rep := m.Check(context.Background()); len(rep.Evidence) != 0 {
		t.Fatalf("second check = %+v, want the same log lines not counted again", rep)
	}
}

func TestAnUnwritableArchiveIsFailed(t *testing.T) {
	h := newHost(t)
	failing, failed := &episode{}, &episode{}
	c := &clock{t0}
	probe := syscall.EROFS
	m := h.monitor(c, &runner{}, failing, failed, func(o *diskhealth.Options) {
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
		o.Probe = func(string) error { return &os.PathError{Op: "open", Path: "probe", Err: probe} }
	})
	rep := m.Check(context.Background())
	if rep.State != diskhealth.Failed || failed.begun != 1 || failing.begun != 1 {
		t.Fatalf("report = %+v, failed begun %d, failing begun %d; want failed and both begun", rep, failed.begun, failing.begun)
	}
	c.t = c.t.Add(30 * time.Minute)
	probe = 0
	m.Check(context.Background()) // the probe is the only check; it passes again
	if failed.ended != 1 || failing.ended != 0 {
		t.Fatalf("failed ended %d, failing ended %d; want failed over, failing still on", failed.ended, failing.ended)
	}
}

func TestFailingEndsAfterADayOfHealthAndIsRaisedOncePerEpisode(t *testing.T) {
	h := newHost(t)
	c := &clock{t0}
	bad := true
	failing, failed := &episode{}, &episode{}
	m := h.monitor(c, &runner{}, failing, failed, func(o *diskhealth.Options) {
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
	})
	check := func() {
		if bad {
			m.RecordWrite(syscall.EIO)
		}
		m.Check(context.Background())
	}
	check()
	c.t = c.t.Add(30 * time.Minute)
	check()
	if failing.begun != 1 {
		t.Fatalf("failing begun %d, want once per episode", failing.begun)
	}
	bad = false
	for c.t = c.t.Add(30 * time.Minute); c.t.Before(t0.Add(30*time.Minute + 24*time.Hour)); c.t = c.t.Add(30 * time.Minute) {
		check()
	}
	if failing.ended != 0 {
		t.Fatalf("failing ended before a full day of health")
	}
	c.t = t0.Add(30*time.Minute + 24*time.Hour)
	check()
	if failing.ended != 1 {
		t.Fatalf("failing ended %d, want 1 after 24 h healthy", failing.ended)
	}
}

func TestRunChecksAtStartAndThenAtMostEveryHalfHour(t *testing.T) {
	h := newHost(t)
	r := &runner{}
	ticks := make(chan time.Time)
	m := h.monitor(&clock{t0}, r, &episode{}, &episode{}, func(o *diskhealth.Options) {
		o.Every = time.Millisecond // asked for more often than allowed
		o.Tick = func(d time.Duration) (<-chan time.Time, func()) {
			if d != 30*time.Minute {
				t.Errorf("interval = %v, want 30m", d)
			}
			return ticks, func() {}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for m.Report().CheckedAt.IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.Report().CheckedAt.IsZero() {
		t.Fatal("no check at start")
	}
	cancel()
	<-done
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

func TestAnArchiveOnLVMIsTracedToItsDisk(t *testing.T) {
	h := newHost(t)
	// Move the archive's mount to 253:2, an LVM volume on nvme0n1p2.
	raw, err := os.ReadFile(h.mountinfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.mountinfo, []byte(strings.Replace(string(raw), " 8:17 ", " 253:2 ", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(h.sys, "devices", "pci0000:00", "nvme", "nvme0", "nvme0n1")
	part := filepath.Join(disk, "nvme0n1p2")
	dm := filepath.Join(h.sys, "devices", "virtual", "block", "dm-2")
	for _, d := range []string{part, filepath.Join(dm, "slaves")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(part, "partition"), []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(part, filepath.Join(dm, "slaves", "nvme0n1p2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dm, filepath.Join(h.sys, "dev", "block", "253:2")); err != nil {
		t.Fatal(err)
	}
	r := &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/nvme0n1": "smart-nvme-worn.json"}}
	rep := h.monitor(&clock{t0}, r, &episode{}, &episode{}).Check(context.Background())
	if rep.Device != "/dev/nvme0n1" || rep.State != diskhealth.Failing {
		t.Fatalf("report = %+v after %v, want the NVMe disk under the volume, failing", rep, r.calls)
	}
}

// The hive's own wiring: zero options are the real system, and before the
// first check the report is unknown without a time.
func TestARealMonitorIsUnknownUntilItsFirstCheck(t *testing.T) {
	m := diskhealth.New(diskhealth.Options{ArchiveDir: t.TempDir()})
	if r := m.Report(); r.State != diskhealth.Unknown || !r.CheckedAt.IsZero() || len(r.Evidence) != 0 {
		t.Fatalf("report before a check = %+v", r)
	}
	if r := m.Check(context.Background()); r.CheckedAt.IsZero() || r.State == diskhealth.Failed {
		t.Fatalf("a check of a temp directory = %+v", r)
	}
}

// #155 review: critical_warning bit 0x02 is the temperature; heat is shown,
// never evidence of failing.
func TestAHotNVMeIsNotFailing(t *testing.T) {
	h := newHost(t)
	r := &runner{out: map[string]string{"smartctl -j -n standby -H -A /dev/sdb": "smart-nvme-hot.json", "journalctl": "journal-empty.txt"}}
	rep := h.monitor(&clock{t0}, r, &episode{}, &episode{}).Check(context.Background())
	if rep.State != diskhealth.OK || rep.TemperatureC == nil || *rep.TemperatureC != 79 {
		t.Fatalf("report = %+v, want ok at 79 °C", rep)
	}
}

// #155 review: a file system whose device cannot be traced (btrfs
// subvolumes, ZFS, mergerfs, NFS) is unknown: its kernel log lines cannot
// be matched, so their absence says nothing.
func TestAnUntraceableDeviceIsUnknown(t *testing.T) {
	h := newHost(t)
	raw, err := os.ReadFile(h.mountinfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.mountinfo, []byte(strings.Replace(string(raw), " 8:17 ", " 0:51 ", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &runner{out: map[string]string{"journalctl": "journal-empty.txt"}}
	rep := h.monitor(&clock{t0}, r, &episode{}, &episode{}).Check(context.Background())
	if rep.State != diskhealth.Unknown {
		t.Fatalf("state = %s after %v, want unknown", rep.State, r.calls)
	}
}

// #155 review: what goes to the cloud keeps the signals and numbers, not
// the detail lines, which carry file paths and log text.
func TestTheCloudGetsTheEvidenceWithoutDetails(t *testing.T) {
	var data any
	failing := &episode{}
	h := newHost(t)
	m := h.monitor(&clock{t0}, &runner{}, failing, &episode{}, func(o *diskhealth.Options) {
		o.LookPath = func(string) (string, error) { return "", errors.New("none") }
	})
	m.RecordWrite(&os.PathError{Op: "write", Path: "/mnt/cameras/camhive/archive/cam_a/x.mp4", Err: syscall.EIO})
	m.Check(context.Background())
	data = failing.data
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	out := string(diskhealth.ForCloud(raw))
	if strings.Contains(out, "/mnt/cameras") || !strings.Contains(out, `"signal":"writes"`) || !strings.Contains(out, `"eio":1`) {
		t.Fatalf("for the cloud: %s", out)
	}
}
