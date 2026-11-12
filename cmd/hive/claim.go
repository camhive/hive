package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivelink"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/version"
)

// runClaim binds this hive to a Camhive account: it prints a code the
// owner enters in the web app and waits for the certificate.
func runClaim(paths hiveconfig.Paths, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("hive claim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	api := fs.String("api", envOr("CAMHIVE_API_URL", hivelink.DefaultAPI), "cloud API base URL")
	serverName := fs.String("gateway-server-name", "", "expected gateway certificate name (development gateways reached by IP)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	key, _, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	link, err := hivelink.Claim(ctx, paths, key, hivelink.ClaimOptions{API: *api, Out: stdout})
	switch {
	case errors.Is(err, hivelink.ErrAlreadyLinked):
		st, _ := hivelink.Load(paths)
		fmt.Fprintf(stderr, "already claimed as %s (%s); remove %s to re-claim\n", st.Link.HiveID, st.Link.APIURL, paths.Link)
		return 1
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *serverName != "" {
		link.ServerName = *serverName
		certPEM, _ := os.ReadFile(paths.Cert) //nolint:gosec // our own path
		caPEM, _ := os.ReadFile(paths.CACert) //nolint:gosec // our own path
		if err := hivelink.Save(paths, link, certPEM, caPEM); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	fmt.Fprintln(stdout, "restart the service to connect: hive restart")
	return 0
}

// heartbeatInterval is how often the hive reports its health summary.
const heartbeatInterval = 30 * time.Second

// startLink runs the cloud tunnel when the hive is claimed; an unclaimed
// hive keeps working on the LAN and logs how to claim.
func startLink(ctx context.Context, paths hiveconfig.Paths, key ed25519.PrivateKey, agent *hiveagent.Agent, relay *hiveapp.RelayListener, log *slog.Logger) {
	st, err := hivelink.Load(paths)
	if err != nil {
		if errors.Is(err, hivelink.ErrNotClaimed) {
			log.Info("not claimed; cloud features are off until `hive claim` is run")
		} else {
			log.Error("cloud link unreadable", "err", err)
		}
		return
	}
	if relay != nil {
		// The cloud relay pipes clients into the TLS API (tunnel.open) through an
		// in-process listener, so the API knows these requests are remote.
		agent.SetRelay(hiveagent.Relay{CA: st.CA, Cert: st.Cert, Key: key, Dial: relay.Dial})
	}
	go func() {
		err := hivelink.Run(ctx, paths, key, hivelink.RunOptions{
			Version: version.Current().String(),
			Logger:  log.With("component", "cloud"),
			Handler: agent.Handle,
			OnEmitter: func(emit func(context.Context, protocol.Event) error) {
				go agent.Run(ctx, emit, heartbeatInterval)
				go agent.CertLoop(ctx, 6*time.Hour) // asks the cloud for a public certificate, renews in time
			},
			ReconnectMin: time.Second, ReconnectMax: time.Minute,
			OnConnect:     agent.RequestCertIfNeeded, // a redeployed cloud may issue differently
			OnStateChange: func(st hivelink.State) { log.Info("device certificate renewed", "not_after", st.Cert.NotAfter) },
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("cloud link stopped", "err", err)
		}
	}()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
