// Command hive-updater is the Hive's service process (ADR-0018 §4): it
// runs the current Hive version as a child, restarts it when it dies, and
// installs signed updates with a health check and a rollback. It has no
// cloud link and no camera code; see docs/security/updates.md.
package main

import (
	"context"
	"errors"
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
	sup, err := assemble(p.log, hiveconfig.DefaultPaths(), journal.DefaultDir(), executableDir(), version.Current())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		go watchRequests(ctx, journal.DefaultDir(), sup, p.log)
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
	if d := os.Getenv("CAMHIVE_DATA_DIR"); d != "" {
		paths = paths.WithDataDir(d)
	}
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
	window, err := update.ParseWindow(cfg.UpdateWindow)
	if err != nil {
		return nil, err
	}
	sc := update.SupervisorConfig{
		Versions: versions, Runner: update.ExecRunner{}, Health: update.HTTPHealth("http://"+cfg.ListenAddr, nil),
		Hive: hiveID(paths), Updater: own, Window: window, Mode: update.Mode(cfg.Updates), Logger: log,
		Report: func(kind string, data map[string]any) {
			if err := journal.Append(dir, kind, data); err != nil {
				log.Error("update journal", "err", err)
			}
		},
	}
	if built, err := time.Parse("2006-01-02", build.Date); err == nil {
		sc.NotBefore = built
	} else if t, err := time.Parse(time.RFC3339, build.Date); err == nil {
		sc.NotBefore = t
	}
	root, err := trustedRoot(cfg)
	switch {
	case err != nil:
		return nil, err
	case !release:
		log.Info("development build: updates are off")
	case root == nil:
		log.Warn("no update root in this build: updates are off")
	default:
		sc.Source = &update.SourceChannel{Platform: update.Platform(), Source: update.Source{
			MetadataURL: cfg.UpdateURL + "/" + cfg.UpdateChannel, Root: root, CacheDir: filepath.Join(dir, "tuf")}}
	}
	return update.NewSupervisor(sc), nil
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

// requestFile is written by `hive update`; the updater acts on it within a
// minute. It lives in the private updater directory: only root can ask.
const requestFile = "update-request"

func watchRequests(ctx context.Context, dir string, sup *update.Supervisor, log *slog.Logger) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := filepath.Join(dir, requestFile)
		if err := os.Remove(p); err == nil {
			log.Info("update requested by the owner")
			sup.AskNow(ctx)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("update request", "err", err)
		}
	}
}
