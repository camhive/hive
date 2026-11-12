// Command hive is the Camhive agent installed on the customer's server. It
// runs as an OS service (systemd, Windows Service, launchd):
//
//	hive install | uninstall | start | stop | status   manage the OS service
//	hive run                                          run in the foreground
//	hive diag                                         write a diagnostic bundle
//	hive --version
//
// Files live in the OS-conventional data directory (see hiveconfig), or under
// CAMHIVE_DATA_DIR when set; the service is installed with that environment.
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
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
	if cmd == "--version" || cmd == "version" {
		fmt.Fprintln(stdout, "camhive-hive", version.Current())
		return 0
	}

	paths := hiveconfig.DefaultPaths()
	if dir := os.Getenv("CAMHIVE_DATA_DIR"); dir != "" {
		paths = paths.WithDataDir(dir)
	}
	if cmd == "diag" {
		return runDiag(paths, stdout, stderr)
	}
	if cmd == "claim" {
		return runClaim(paths, args[1:], stdout, stderr)
	}
	prg := &program{log: slog.New(slog.NewTextHandler(stderr, nil)), paths: paths}
	svcCfg := &service.Config{
		Name:        serviceName,
		DisplayName: "Camhive Hive",
		Description: "Camhive video surveillance agent: records cameras and keeps footage at home.",
		Arguments:   []string{"run"},
		Option: service.KeyValue{
			"Restart": "always", // systemd: restart on failure and on clean exit alike
		},
	}
	if dir := os.Getenv("CAMHIVE_DATA_DIR"); dir != "" {
		svcCfg.EnvVars = map[string]string{"CAMHIVE_DATA_DIR": dir} // carried into the installed service
	}
	svc, err := service.New(prg, svcCfg)
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
		st, err := svc.Status()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, statusString(st))
		return 0
	case "install", "uninstall", "start", "stop", "restart":
		if err := service.Control(svc, cmd); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s ok\n", serviceName, cmd)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: hive [run|claim|install|uninstall|start|stop|restart|status|diag|--version]\n", cmd)
		return 2
	}
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

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	log := p.log.With("hive_id", id)
	parts, err := assemble(ctx, cfg, p.paths, key, string(id), version.Current().String(), log)
	if err != nil {
		cancel()
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
		go parts.index.Run(ctx, parts.archive, time.Minute)
		go parts.sweeper.Run(ctx, 5*time.Minute)
		go parts.health.Run(ctx)
	}
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
