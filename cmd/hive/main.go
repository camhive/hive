// Command hive is the Camhive agent installed on the customer's server. It
// runs as an OS service (systemd, Windows Service, launchd):
//
//	hive install | uninstall | start | stop | status   manage the OS service
//	hive run                                          run in the foreground
//	hive diag                                         write a diagnostic bundle
//	hive move-data <new-dir> | --cancel               move the data to a new disk (#155)
//	hive version                                      this binary and the running hive
//	hive --version                                    this binary, one line
//
// Files live in the OS-conventional data directory (see hiveconfig), or under
// CAMHIVE_DATA_DIR when set; the service is installed with that environment,
// and commands run without it use the installed service's (Linux).
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivetls"
	"github.com/camhive/hive/internal/logging"
	"github.com/camhive/hive/internal/recordcheck"
	"github.com/camhive/hive/internal/update/journal"
	"github.com/camhive/hive/internal/version"
)

const serviceName = "camhive-hive"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd == "--version" { // one line, for scripts
		fmt.Fprintln(stdout, "camhive-hive", version.Current())
		return 0
	}

	paths := hiveconfig.DefaultPaths()
	if os.Getenv("CAMHIVE_DATA_DIR") == "" && cmd != "run" {
		if dir := unitDataDir(systemdUnit); dir != "" {
			_ = os.Setenv("CAMHIVE_DATA_DIR", dir) // sudo drops it; a reinstall keeps it
		}
	}
	if dir := os.Getenv("CAMHIVE_DATA_DIR"); dir != "" {
		paths = paths.WithDataDir(dir)
	}
	if code, ok := standalone(cmd, args, paths, stdout, stderr); ok {
		return code
	}
	prg := &program{log: slog.New(slog.NewTextHandler(stderr, nil)), paths: paths}
	svc, err := service.New(prg, serviceConfig(cmd))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch cmd {
	case "run":
		if err := svc.Run(); err != nil { // blocks; handles service control on Windows and signals elsewhere
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "status":
		return runStatus(svc.Status, version.Current(), paths, stdout, stderr)
	case "install", "uninstall", "start", "stop", "restart":
		if err := service.Control(svc, cmd); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		afterInstall(cmd, paths, stderr)
		fmt.Fprintf(stdout, "%s: %s ok\n", serviceName, cmd)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: hive [run|claim|install|uninstall|start|stop|restart|status|update|diag|local-login|move-data|version|--version]\n", cmd)
		return 2
	}
}

// afterInstall tells an installed systemd service to wait for its disks
// (#172): the hive's own drop-in.
func afterInstall(cmd string, paths hiveconfig.Paths, stderr io.Writer) {
	if cmd != "install" || !systemdHere() {
		return
	}
	if err := installMounts(systemdUnit, runSystemctl, paths); err != nil {
		fmt.Fprintln(stderr, "the service could not be told to wait for its disks:", err)
	}
}

// serviceConfig describes the camhive-hive service. On install it runs
// hive-updater, which runs this hive as its child (ADR-0018); without the
// updater next to us (a bare binary), the hive itself.
func serviceConfig(cmd string) *service.Config {
	c := &service.Config{
		Name:        serviceName,
		DisplayName: "Camhive Hive",
		Description: "Camhive video surveillance agent: records cameras and keeps footage at home.",
		Arguments:   []string{"run"},
		Option: service.KeyValue{
			"Restart": "always", // systemd: restart on failure and on clean exit alike
		},
	}
	if dir := os.Getenv("CAMHIVE_DATA_DIR"); dir != "" {
		c.EnvVars = map[string]string{"CAMHIVE_DATA_DIR": dir} // carried into the installed service
	}
	if up := updaterNextTo(); cmd == "install" && up != "" {
		c.Executable = up
	}
	return c
}

// standalone runs the commands that do not touch the service.
func standalone(cmd string, args []string, paths hiveconfig.Paths, stdout, stderr *os.File) (int, bool) {
	switch cmd {
	case "diag":
		return runDiag(paths, stdout, stderr), true
	case "claim":
		return runClaim(paths, args[1:], stdout, stderr), true
	case "update":
		return requestUpdate(journal.DefaultDir(), stdout, stderr), true
	case "local-login":
		return runLocalLogin(paths, args[1:], os.Stdin, stdout, stderr), true
	case "move-data":
		return runMoveData(paths, args[1:], stdout, stderr), true
	case "version":
		reportVersion(context.Background(), stdout, stderr, version.Current(), paths)
		return 0, true
	}
	return 0, false
}

func statusString(st service.Status) string {
	switch st {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// program adapts hiveapp to the service lifecycle.
type program struct {
	log      *slog.Logger
	closeLog func() error
	paths    hiveconfig.Paths
	cancel   context.CancelFunc
	done     chan struct{}
}

func (p *program) Start(_ service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	if err := identityGuard(os.Getenv("CAMHIVE_DATA_DIR"), systemdUnit, otherFS); err != nil {
		// #172: not a new, unclaimed hive beside a missing disk, and no exit
		// hive-updater would read as a failed update: wait for the disk.
		p.log.Error("waiting for the data directory's disk", "err", err)
		go p.waitForDisk(ctx, err)
		return nil
	}
	if err := p.start(ctx); err != nil {
		cancel()
		return err
	}
	return nil
}

// start loads the configuration and the identity, assembles the hive and
// serves it until ctx ends.
func (p *program) start(ctx context.Context) error {
	cfg, firstRun, err := hiveconfig.Load(p.paths)
	if err != nil {
		return err
	}
	key, created, err := hiveconfig.LoadOrCreateKey(p.paths)
	if err != nil {
		return err
	}
	logger, closeLog, err := logging.New(logging.Config{
		Level: cfg.LogLevel, File: filepath.Join(p.paths.Logs, "hive.log"),
		Console: os.Stderr, ConsoleJSON: !service.Interactive(),
	})
	if err != nil {
		return err
	}
	p.log, p.closeLog = logger, closeLog
	id, domains := effectiveIdentity(p.paths, key.Public().(ed25519.PublicKey))
	if firstRun || created {
		p.log.Info("first run", "hive_id", id, "data_dir", p.paths.Data, "config", p.paths.Config)
	}

	log := p.log.With("hive_id", id)
	p.waitForDisksNextTime(cfg, log)
	parts, err := assemble(ctx, cfg, p.paths, key, string(id), version.Current().String(), log)
	if err != nil {
		return err
	}
	appCfg := hiveapp.Config{ListenAddr: cfg.ListenAddr, Version: version.Current().String(), Logger: log, Handler: parts.handler}
	var relay *hiveapp.RelayListener
	if cfg.TLSListenAddr != "" {
		if cert, err := domains.LoadOrCreate(p.paths.PublicCert, p.paths.PublicKey, string(id)); err != nil {
			log.Error("public certificate", "err", err)
		} else {
			holder := hivetls.NewHolder(cert)
			appCfg.TLSListenAddr, appCfg.TLS = cfg.TLSListenAddr, holder.ServerConfig()
			_, port, _ := net.SplitHostPort(cfg.TLSListenAddr)
			tlsPort, _ := strconv.Atoi(port)
			parts.agent.SetTLS(hiveagent.TLSInfo{HiveID: string(id), Domains: domains, Holder: holder, CertPath: p.paths.PublicCert, KeyPath: p.paths.PublicKey, Port: tlsPort})
			if rl, err := hiveapp.NewRelayListener(ctx); err != nil {
				log.Error("relay listener", "err", err)
			} else {
				relay = rl // the relay pipes in here; its requests count as remote
				appCfg.Relay = relay
			}
			log.Info("public certificate", "names", cert.Leaf.DNSNames, "issuer", hivetls.IssuerOf(cert), "fingerprint_sha256", hivetls.Fingerprint(cert), "not_after", cert.Leaf.NotAfter)
		}
	}
	app := hiveapp.New(appCfg)
	startLink(ctx, p.paths, key, parts.agent, relay, log)
	go p.serve(ctx, app, parts, log)
	return nil
}

// serve runs ingest and the API until ctx is cancelled (or, in the
// foreground, until Ctrl-C / SIGTERM), then releases the parts.
func (p *program) serve(ctx context.Context, app *hiveapp.App, parts *runtimeParts, log *slog.Logger) {
	defer close(p.done)
	defer parts.close()
	if service.Interactive() {
		sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx = sigCtx
	}
	if parts.ingest != nil {
		go func() {
			if err := parts.ingest.Run(ctx); err != nil {
				log.Error("ingest stopped", "err", err)
			}
		}()
		go parts.index.Run(ctx, parts.archive, recordcheck.IndexInterval)
		go sealLoop(ctx, parts.vault, parts.index, parts.archive, parts.exports, parts.failedWrite, log.With("component", "archivekey"))
		go parts.disk.Run(ctx) // #155: at start and every 30 minutes
		go parts.sweeper.Run(ctx, 5*time.Minute)
		go parts.health.Run(ctx)
		go parts.care.Run(ctx, careEvery)
		go parts.locator.Run(ctx, time.Minute)
		motionCtx, stopMotion := context.WithCancel(ctx)
		motionDone := make(chan struct{})
		go func() { parts.motion.Run(motionCtx); close(motionDone) }()
		defer func() { stopMotion(); <-motionDone }() // it ends open episodes before the store closes
		go parts.cameras.Run(ctx)
	}
	go parts.recording.Run(ctx)                            // #172: once a minute, with or without the video engine
	go hiveapp.RunAutolock(ctx, parts.deps, autolockEvery) // #147: works without the video engine too
	go reportUpdates(ctx, journal.DefaultDir(), parts.store, parts.agent, log)
	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("hive exited", "err", err)
	}
}

func (p *program) Stop(_ service.Service) error {
	p.cancel()
	<-p.done
	if p.closeLog != nil {
		_ = p.closeLog()
	}
	return nil
}
