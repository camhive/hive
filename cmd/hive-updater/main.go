// Command hive-updater is the Hive's service process (ADR-0018 §4): it
// runs the current Hive version as a child, restarts it when it dies, and
// installs signed updates with a health check and a rollback. It has no
// cloud link and no camera code; see docs/security/updates.md.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivelink"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/update/journal"
	"github.com/camhive/hive/internal/version"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version", "--version":
		fmt.Fprintln(stdout, "camhive-hive-updater", version.Current())
		return 0
	case "run":
		prg := &program{log: slog.New(slog.NewTextHandler(stderr, nil))}
		svc, err := service.New(prg, &service.Config{Name: "camhive-hive"})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := svc.Run(); err != nil { // service control on Windows, signals elsewhere
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: hive-updater [run|version]\n", cmd)
		return 2
	}
}

// program adapts the supervisor to the service lifecycle.
type program struct {
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(_ service.Service) error {
	paths := dataPaths(hiveconfig.DefaultPaths(), os.Getenv("CAMHIVE_DATA_DIR"))
	sup, err := assemble(p.log, paths, journal.DefaultDir(), executableDir(), version.Current())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		go newWatcher(journal.DefaultDir(), paths, sup, p.log).run(ctx)
		sup.Run(ctx)
	}()
	return nil
}

func (p *program) Stop(_ service.Service) error {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	return nil
}

// dataPaths honours CAMHIVE_DATA_DIR, which the service sets when the data
// lives elsewhere (a USB disk).
func dataPaths(def hiveconfig.Paths, dir string) hiveconfig.Paths {
	if dir == "" {
		return def
	}
	return def.WithDataDir(dir)
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// assemble builds the supervisor: the private store seeded with the bundle
// next to this executable, the hive's settings, the trusted root.
func assemble(log *slog.Logger, paths hiveconfig.Paths, dir, bundleDir string, build version.Info) (*update.Supervisor, error) {
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil {
		return nil, err
	}
	if err := update.EnsurePrivate(dir); err != nil {
		return nil, err
	}
	versions := update.OpenVersions(dir)
	own, err := update.ParseVersion(build.Version)
	release := err == nil
	if !release {
		own, _ = update.ParseVersion("0.0.0-dev") // a local build: run it, never update it
	}
	if err := versions.Seed(own, bundleDir); err != nil {
		return nil, fmt.Errorf("seed %s from %s: %w", own, bundleDir, err)
	}
	if err := journal.Announce(dir, own.String()); err != nil {
		return nil, err
	}
	pol := newPolicies(paths, dir, release, log)
	first, err := pol.from(cfg)
	if err != nil {
		return nil, err
	}
	pol.last = first
	verdict := launchVerdict(release, first.Source != nil)
	sc := update.SupervisorConfig{
		Versions: versions, Runner: update.ExecRunner{Env: []string{update.LaunchedEnv + "=" + verdict}}, Health: update.HTTPHealth("http://"+cfg.ListenAddr, nil),
		Hive: hiveID(paths), Updater: own, Policy: pol.current, Logger: log,
		Report: func(kind string, data map[string]any) {
			if err := journal.Append(dir, kind, data); err != nil {
				log.Error("update journal", "err", err)
			}
		},
	}
	sc.NotBefore = builtAt(build.Date)
	logUpdates(log, cfg, verdict)
	return update.NewSupervisor(sc), nil
}

// builtAt is the build date, or zero when the build has none.
func builtAt(date string) time.Time {
	if t, err := time.Parse("2006-01-02", date); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, date)
	return t
}

// launchVerdict is whether this updater installs updates, as the hive it
// starts learns it (update.LaunchedEnv).
func launchVerdict(release, source bool) string {
	switch {
	case !release:
		return "dev_build"
	case !source:
		return "no_root"
	}
	return "on"
}

// logUpdates says at start whether this updater installs updates.
func logUpdates(log *slog.Logger, cfg hiveconfig.Config, verdict string) {
	switch verdict {
	case "dev_build":
		log.Info("development build: updates are off")
	case "no_root":
		log.Warn("no update root in this build: updates are off")
	default:
		log.Info("updates on", "url", cfg.UpdateURL+"/"+cfg.UpdateChannel, "mode", cfg.Updates, "window", cfg.UpdateWindow)
	}
}

// trustedRoot is update_root from hive.yaml when set, else the built-in one.
func trustedRoot(cfg hiveconfig.Config) ([]byte, error) {
	if cfg.UpdateRoot == "" {
		return update.ShippedRoot(), nil
	}
	return os.ReadFile(cfg.UpdateRoot)
}

// hiveID is the claimed id (rollout buckets follow it); "" before claim.
func hiveID(paths hiveconfig.Paths) string {
	st, err := hivelink.Load(paths)
	if err != nil {
		return ""
	}
	return st.Link.HiveID
}

// checker is the part of the supervisor the watcher drives.
type checker interface {
	AskNow(ctx context.Context)
	CheckNow(ctx context.Context)
}

// watcher looks every 30 s for an owner's request to update now (`hive
// update`, the app's "Update now") and for changed update settings in
// hive.yaml (#82). It keeps its own view of the settings: the policy Run
// reads is not shared with it.
type watcher struct {
	dir   string
	paths hiveconfig.Paths
	sup   checker
	log   *slog.Logger
	seen  watched
	every time.Duration
}

// watched are the settings whose change calls for a check.
type watched struct {
	hiveconfig.UpdateSettings
	url, root string
}

func newWatcher(dir string, paths hiveconfig.Paths, sup checker, log *slog.Logger) *watcher {
	w := &watcher{dir: dir, paths: paths, sup: sup, log: log, every: 30 * time.Second}
	w.seen, _ = w.settings()
	return w
}

func (w *watcher) settings() (watched, bool) {
	cfg, err := hiveconfig.Read(w.paths)
	if err != nil {
		return watched{}, false
	}
	return watched{UpdateSettings: cfg.UpdateSettings(), url: cfg.UpdateURL, root: cfg.UpdateRoot}, true
}

func (w *watcher) run(ctx context.Context) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *watcher) tick(ctx context.Context) {
	asked, err := journal.TakeRequest(w.dir)
	if err != nil {
		w.log.Warn("update request", "err", err)
	}
	if asked {
		w.log.Info("update requested by the owner")
		w.sup.AskNow(ctx)
	}
	if now, ok := w.settings(); ok && now != w.seen {
		w.seen = now
		w.log.Info("update settings changed", "mode", now.Updates, "window", now.UpdateWindow, "channel", now.UpdateChannel)
		if !asked { // an asked check already read them
			w.sup.CheckNow(ctx)
		}
	}
}
