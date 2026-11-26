package diskhealth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MinEvery is the shortest time between two checks: smartctl wakes a
// sleeping disk, and the verdict is about days, not minutes.
const MinEvery = 30 * time.Minute

// Episode is a lasting condition the monitor begins and ends
// (events.Condition: disk.failing, disk.failed).
type Episode interface {
	Begin(ctx context.Context, at time.Time, data any)
	End(ctx context.Context, at time.Time)
}

// Runner runs a command and returns its standard output, also when it
// exits non-zero (smartctl's exit status is a bit mask of warnings).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Options configure a Monitor. Zero values are the real system.
type Options struct {
	ArchiveDir string
	EngineLog  string        // the engine's log, read for segment write errors; "" none
	Every      time.Duration // never below MinEvery
	Failing    Episode       // disk.failing; nil: none
	Failed     Episode       // disk.failed; nil: none
	Logger     *slog.Logger

	// For tests.
	GOOS        string
	Now         func() time.Time
	Run         Runner
	LookPath    func(string) (string, error)
	Mountinfo   string // default /proc/self/mountinfo
	SysDevBlock string // default /sys/dev/block
	Probe       func(dir string) error
	StatDev     func(dir string) (uint64, error) // the device number of the file system holding dir
	Tick        func(time.Duration) (<-chan time.Time, func())
}

// Monitor checks the archive disk and keeps the last report.
type Monitor struct {
	o Options

	mu       sync.Mutex // the report and the writes recorded since the last check
	report   Report
	writes   Writes
	checking sync.Mutex // one check at a time; it owns the fields below
	last     time.Time  // the previous check
	logAt    int64      // how far the engine's log was read
	dmesg    int        // kernel errors dmesg showed at the previous check
	ep       episodes
	found    bool   // the device lookup has worked once
	rootSeen bool   // the archive directory existed at a check
	rootDev  uint64 // its device number then
}

// New makes a Monitor.
func New(o Options) *Monitor {
	o.defaults()
	return &Monitor{o: o, report: Report{State: Unknown}}
}

// defaults fills what was left zero with the real system.
func (o *Options) defaults() {
	o.Every = max(o.Every, MinEvery)
	o.GOOS = cmp.Or(o.GOOS, runtime.GOOS)
	o.Mountinfo = cmp.Or(o.Mountinfo, "/proc/self/mountinfo")
	o.SysDevBlock = cmp.Or(o.SysDevBlock, "/sys/dev/block")
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Run == nil {
		o.Run = run
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Probe == nil {
		o.Probe = probe
	}
	if o.StatDev == nil {
		o.StatDev = StatDev
	}
	if o.Tick == nil {
		o.Tick = func(d time.Duration) (<-chan time.Time, func()) { t := time.NewTicker(d); return t.C, t.Stop }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}

// Report is the last check's report; Unknown before the first.
func (m *Monitor) Report() Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.report
	r.Evidence = append([]Evidence(nil), r.Evidence...)
	return r
}

// RecordWrite notes a failed write of the hive's into the archive or the
// database; the next check counts it. Errors that say nothing about the
// disk are ignored.
func (m *Monitor) RecordWrite(err error) {
	c := Classify(err)
	if c == None {
		return
	}
	m.mu.Lock()
	m.writes.add(c, err.Error())
	m.mu.Unlock()
}

// Run checks now and then every Every until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	tick, stop := m.o.Tick(m.o.Every)
	defer stop()
	for {
		m.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// Check reads every signal once, keeps the report and moves the episodes.
func (m *Monitor) Check(ctx context.Context) Report {
	m.checking.Lock()
	defer m.checking.Unlock()
	now := m.o.Now()
	s, dev, temp := m.sample(ctx, now)
	st, ev := Evaluate(s)
	rep := Report{State: st, CheckedAt: now, Device: dev, TemperatureC: temp, Evidence: ev}
	m.mu.Lock()
	m.report = rep
	m.mu.Unlock()
	var changes []change
	m.ep, changes = m.ep.step(verdict{state: st, kernelOnly: kernelOnly(ev), clean: s.clean()}, now)
	for _, c := range changes {
		m.apply(ctx, c, rep)
	}
	if st == Failing || st == Failed {
		m.o.Logger.Warn("archive disk "+string(st), "device", dev, "evidence", ev)
	}
	m.last = now
	return rep
}

func (m *Monitor) apply(ctx context.Context, c change, rep Report) {
	ep := m.o.Failing
	if c.failed {
		ep = m.o.Failed
	}
	switch {
	case ep == nil:
	case c.begin:
		ep.Begin(ctx, rep.CheckedAt, map[string]any{"device": rep.Device, "evidence": rep.Evidence})
	default:
		ep.End(ctx, rep.CheckedAt)
	}
}

// sample reads the signals of one check.
func (m *Monitor) sample(ctx context.Context, now time.Time) (Sample, string, *int) {
	var s Sample
	var dev string
	var temp *int
	if m.o.GOOS == "linux" {
		if d, err := findDevice(m.o.Mountinfo, m.o.SysDevBlock, m.o.ArchiveDir); err != nil {
			m.o.Logger.Debug("archive device", "err", err)
			if m.found {
				s.Lost = append(s.Lost, "the archive's device can no longer be found: "+err.Error())
			}
		} else {
			m.found = true
			s.SMART, dev, temp = m.smart(ctx, d)
			s.Kernel = m.kernel(ctx, d, now)
			if dev == "" && len(d.Disks) > 0 {
				dev = d.Disks[0]
			}
		}
	}
	m.mu.Lock()
	s.Writes, m.writes = m.writes, Writes{}
	m.mu.Unlock()
	m.engineLog(&s.Writes)
	if lost := m.root(); lost != "" {
		s.Lost = append(s.Lost, lost)
	}
	if err := m.o.Probe(m.o.ArchiveDir); err != nil {
		s.Probe, s.ProbeFailed = Classify(err), true
		s.Writes.add(s.Probe, "probe: "+err.Error())
	}
	return s, dev, temp
}

// root checks the archive directory against what the first check that
// found it saw (#172): gone, or on another device, is a lost archive. A
// directory that never existed is no archive yet.
func (m *Monitor) root() string {
	dev, err := m.o.StatDev(m.o.ArchiveDir)
	switch {
	case err == nil && !m.rootSeen:
		m.rootSeen, m.rootDev = true, dev
	case err == nil && dev != m.rootDev:
		return fmt.Sprintf("the archive directory is on another device (%#x) than when it was first seen (%#x)", dev, m.rootDev)
	case err != nil && (m.rootSeen && Gone(err) || Classify(err).disk()):
		return "the archive directory vanished: " + err.Error()
	}
	return ""
}

// Gone says an error is a path that is not there (any more): ENOENT,
// ENOTDIR, or ESTALE from a mount that went away. Alone it says nothing
// about a disk; on a path that existed before it does (#172).
func Gone(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ESTALE)
}

// smart asks smartctl about every disk under the archive (several for
// LVM or RAID); the first with evidence stands for the device, else the
// first that answered. nil when none answered.
func (m *Monitor) smart(ctx context.Context, d Device) (*SMART, string, *int) {
	bin, err := m.o.LookPath("smartctl")
	if err != nil {
		return nil, "", nil
	}
	var pick *SMART
	dev := ""
	for _, disk := range d.Disks {
		s, ok := m.smartOne(ctx, bin, disk)
		if ok && (pick == nil || len(pick.evidence()) == 0 && len(s.evidence()) > 0) {
			pick, dev = &s, disk
		}
	}
	if pick == nil {
		return nil, "", nil
	}
	return pick, dev, pick.TemperatureC
}

// smartOne asks about one disk; a USB bridge smartctl does not know is
// asked again as SAT.
func (m *Monitor) smartOne(ctx context.Context, bin, disk string) (SMART, bool) {
	// -n standby: a sleeping disk is not woken; it is asked at the next check.
	out, _ := m.o.Run(ctx, bin, "-j", "-n", "standby", "-H", "-A", disk)
	s, ok, needsType := parseSMART(out)
	if !ok && needsType {
		out, _ = m.o.Run(ctx, bin, "-j", "-n", "standby", "-H", "-A", "-d", "sat", disk)
		s, ok, _ = parseSMART(out)
	}
	if ok && s.Device == "" {
		s.Device = disk
	}
	return s, ok
}

// kernel reads the kernel log since the previous check (a day back at the
// first): the journal when there is one, else dmesg, whose errors since
// boot are counted and compared with the previous count.
func (m *Monitor) kernel(ctx context.Context, d Device, now time.Time) *Kernel {
	if len(d.Names) == 0 {
		return nil // a device sysfs does not know (btrfs, ZFS, NFS): its lines cannot be matched
	}
	since := m.last
	if since.IsZero() {
		since = now.Add(-24 * time.Hour)
	}
	if bin, err := m.o.LookPath("journalctl"); err == nil {
		out, err := m.o.Run(ctx, bin, "-k", "-o", "short-iso", "--no-pager", "-q", "--since", fmt.Sprintf("@%d", since.Unix()))
		if err == nil {
			k := countKernel(string(out), d.Names)
			return &k
		}
	}
	bin, err := m.o.LookPath("dmesg")
	if err != nil {
		return nil
	}
	out, err := m.o.Run(ctx, bin)
	if err != nil {
		return nil
	}
	k := countKernel(string(out), d.Names)
	total := k.Errors
	if total >= m.dmesg {
		k.Errors = total - m.dmesg
	}
	m.dmesg = total
	return &k
}

// engineLog counts the segment write errors the engine logged since the
// previous check. The engine's log starts empty at each engine start.
func (m *Monitor) engineLog(w *Writes) {
	if m.o.EngineLog == "" {
		return
	}
	f, err := os.Open(m.o.EngineLog)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if st.Size() < m.logAt {
		m.logAt = 0
	}
	if _, err := f.Seek(m.logAt, io.SeekStart); err != nil {
		return
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<20))
	if err != nil {
		return
	}
	end := strings.LastIndexByte(string(b), '\n') + 1 // a line still being written waits for the next check
	m.logAt += int64(end)
	for line := range strings.Lines(string(b[:end])) {
		w.add(classifyText(line), strings.TrimSpace(line))
	}
}

// probe writes, syncs and removes a small file in the archive directory.
func probe(dir string) error { return WriteProbe(dir, ".diskhealth-probe") }

// WriteProbe writes, syncs and removes the small file name in dir; nil
// when dir does not exist (nothing to say about the disk). Each caller
// uses its own name, so two probes never remove each other's file.
func WriteProbe(dir, name string) error {
	if _, err := os.Stat(dir); err != nil {
		return nil //nolint:nilerr // no archive directory yet: nothing to say about the disk
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // our archive directory
	if err != nil {
		return err
	}
	_, werr := f.Write(make([]byte, 4096))
	serr := f.Sync()
	cerr := f.Close()
	rerr := os.Remove(path)
	return errors.Join(werr, serr, cerr, rerr)
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed tools found by LookPath
}
