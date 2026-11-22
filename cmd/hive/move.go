package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/camhive/hive/internal/datamove"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/pki"
)

const moveUsage = "usage: hive move-data <new-dir>   copy the hive to a new disk and run it from there (as root)\n       hive move-data --cancel    give an unfinished move up; the hive records from where it was"

// mover is the move-data command with what it needs from the system.
type mover struct {
	paths   hiveconfig.Paths
	root    func() bool
	systemd func() bool
	svc     datamove.Service // nil: the camhive-hive service
	dropIn  string           // the move's drop-in; "" the real one
	run     func(context.Context, datamove.Options) (datamove.Result, error)
}

func runMoveData(paths hiveconfig.Paths, args []string, stdout, stderr io.Writer) int {
	return mover{paths: paths, root: isRoot, systemd: hasSystemd, run: datamove.Move}.main(args, stdout, stderr)
}

func (m mover) main(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, moveUsage)
		return 2
	}
	switch {
	case !m.root():
		fmt.Fprintln(stderr, "hive move-data: run it as root (sudo): it reads the hive's keys and changes its service")
		return 1
	case !m.systemd():
		fmt.Fprintln(stderr, "hive move-data: it moves a hive run by systemd on Linux; elsewhere stop the hive, copy the data directory by hand and install the service again with CAMHIVE_DATA_DIR (docs: Replace the disk)")
		return 1
	}
	if m.svc == nil {
		m.svc = newSystemService()
	}
	if args[0] == "--cancel" {
		return m.cancel(stdout, stderr)
	}
	if why := datamove.SafePath(args[0]); why != "" {
		fmt.Fprintln(stderr, "hive move-data:", why)
		return 1
	}
	opts, err := m.options(args[0], stdout)
	if err != nil {
		fmt.Fprintln(stderr, "hive move-data:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := m.run(ctx, opts); err != nil {
		fmt.Fprintln(stderr, "hive move-data:", err)
		fmt.Fprintln(stderr, "nothing was deleted; the hive does not record until the move ends: run the same command again to go on, or `hive move-data --cancel`")
		return 1
	}
	return 0
}

// options reads what the move needs from the hive's files.
func (m mover) options(to string, stdout io.Writer) (datamove.Options, error) {
	cfg, err := hiveconfig.Read(m.paths)
	if err != nil {
		return datamove.Options{}, err
	}
	raw, err := os.ReadFile(m.paths.Key)
	if err != nil {
		return datamove.Options{}, fmt.Errorf("the hive's key: %w", err)
	}
	key, err := pki.ParsePrivateKey(raw)
	if err != nil {
		return datamove.Options{}, err
	}
	sk, err := hiveconfig.StoreKey(key)
	if err != nil {
		return datamove.Options{}, err
	}
	_, err = os.Stat(cmp.Or(m.dropIn, systemdUnit+".d/"+dropInName))
	return datamove.Options{From: m.paths, FromOverride: err == nil, ArchiveDir: cfg.ArchiveDir, MinFreeBytes: int64(cfg.MinFreeGB) << 30,
		To: to, StoreKey: sk, Service: m.svc, Progress: stdout}, nil
}

func (m mover) cancel(stdout, stderr io.Writer) int {
	if err := datamove.Cancel(m.paths.Data, m.svc, time.Now()); err != nil {
		fmt.Fprintln(stderr, "hive move-data:", err)
		return 1
	}
	fmt.Fprintln(stdout, "the move is given up; the hive records from where it was again (nothing copied was removed)")
	return 0
}

func isRoot() bool { return os.Geteuid() == 0 }

// hasSystemd: Linux booted with systemd (what sd_booted checks).
func hasSystemd() bool {
	st, err := os.Stat("/run/systemd/system")
	return runtime.GOOS == "linux" && err == nil && st.IsDir()
}

// systemdUnit is where hive install (and the packages, through it) put
// the service definition; dropInName is the move's addition beside it.
const (
	systemdUnit = "/etc/systemd/system/" + serviceName + ".service"
	dropInName  = "data-dir.conf"
)

// dropInFor is the drop-in that runs the hive from dir. The assignment is
// quoted as systemd wants; datamove.SafePath keeps out what quoting would
// not save (%, $, \).
func dropInFor(dir string) string {
	return "[Service]\nEnvironment=\"CAMHIVE_DATA_DIR=" + dir + "\"\n"
}

// osService is camhive-hive under systemd.
type osService struct {
	systemctl func(args ...string) error
	dropIn    string
	answering func() error // the hive's loopback /healthz
	wait      time.Duration
}

func newSystemService() *osService {
	paths := hiveconfig.DefaultPaths()
	return &osService{
		systemctl: func(args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput() //nolint:gosec // fixed arguments
			if err != nil {
				return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		dropIn: systemdUnit + ".d/" + dropInName,
		answering: func() error {
			_, err := askHive(context.Background(), hiveAddr(paths, io.Discard))
			return err
		},
		wait: time.Minute,
	}
}

// Restart restarts the running service so that the hive reads the move's
// note; a stopped one reads it at its next start.
func (s *osService) Restart() error {
	if err := s.systemctl("cat", serviceName); err != nil {
		return fmt.Errorf("the camhive-hive service is not installed (install the package, or run hive install): %w", err)
	}
	if !s.active() {
		return nil // stopped: it reads the note when it starts
	}
	return s.systemctl("restart", serviceName)
}

// Switch points the service at dir with a drop-in, written whole and
// renamed into place (nothing is uninstalled), or at the definition as
// installed when dir is "" (the drop-in removed), restarts it and waits
// until the hive runs and answers. When it does not, the previous drop-in
// (or none) comes back and the hive is started from it again.
func (s *osService) Switch(dir string) error {
	before, err := os.ReadFile(s.dropIn)
	had := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	err = s.apply([]byte(dropInFor(dir)), dir != "")
	if err == nil {
		return nil
	}
	if rerr := s.apply(before, had); rerr != nil {
		return fmt.Errorf("%w; putting the old service definition back failed too: %w", err, rerr)
	}
	return err
}

// apply installs the drop-in (or removes it when !present), reloads and
// restarts the service, and waits for the hive.
func (s *osService) apply(content []byte, present bool) error {
	if err := s.writeDropIn(content, present); err != nil {
		return err
	}
	if err := s.systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := s.systemctl("restart", serviceName); err != nil {
		return err
	}
	return s.running()
}

func (s *osService) writeDropIn(content []byte, present bool) error {
	if !present {
		if err := os.Remove(s.dropIn); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.dropIn), 0o755); err != nil { //nolint:gosec // systemd reads it as any unit file
		return err
	}
	tmp := s.dropIn + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil { //nolint:gosec // a unit file
		return err
	}
	return os.Rename(tmp, s.dropIn)
}

func (s *osService) active() bool { return s.systemctl("is-active", "--quiet", serviceName) == nil }

// running waits until systemd says the service is active and the hive
// answers on loopback.
func (s *osService) running() error {
	deadline := time.Now().Add(s.wait)
	for {
		if s.active() && s.answering() == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the hive did not start from there (journalctl -u camhive-hive says why)")
		}
		time.Sleep(time.Second / 4)
	}
}

// unitDataDir is the CAMHIVE_DATA_DIR the installed unit and its drop-ins
// carry, the last one winning as in systemd; empty without one. Commands
// run by hand (sudo drops the environment) then find the data where the
// service keeps it.
func unitDataDir(unit string) string {
	files := []string{unit}
	drop, _ := filepath.Glob(filepath.Join(unit+".d", "*.conf"))
	sort.Strings(drop)
	dir := ""
	for _, f := range append(files, drop...) {
		if d := envDataDir(f); d != "" {
			dir = d
		}
	}
	return dir
}

// envDataDir is the last Environment=CAMHIVE_DATA_DIR in one unit file.
func envDataDir(path string) string {
	f, err := os.Open(path) //nolint:gosec // the service definition
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	dir := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "Environment=")
		if !ok {
			continue
		}
		if d, ok := strings.CutPrefix(strings.Trim(v, `"`), "CAMHIVE_DATA_DIR="); ok {
			dir = d
		}
	}
	return dir
}

// moveLine is what hive status says about an unfinished move: the hive
// does not record until it ends. Empty when there is none.
func moveLine(dataDir string) string {
	st, err := datamove.Read(dataDir)
	if err != nil {
		return ""
	}
	switch st.State {
	case datamove.Copying:
		pct := 0
		if st.PlannedBytes > 0 {
			pct = int(st.CopiedBytes * 100 / st.PlannedBytes)
		}
		return fmt.Sprintf("move-data: moving to %s (%s, %d%%), not recording until it ends; run `hive move-data %s` to go on or `hive move-data --cancel`", st.To, st.Step, pct, st.To)
	case datamove.Switching:
		return fmt.Sprintf("move-data: the move from %s did not finish switching the service; run `hive move-data %s` again or `hive move-data --cancel`", st.From, st.To)
	case datamove.None, datamove.Done, datamove.Failed:
	}
	return ""
}
