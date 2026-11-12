package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
)

// Handler executes a command on the hive and returns its result.
type Handler func(ctx context.Context, cmd protocol.Command) protocol.Result

// AgentConfig configures the hive side of the tunnel.
type AgentConfig struct {
	URL          string      // wss://gateway/v1/hive
	TLS          *tls.Config // from pki.ClientTLSConfig
	HiveID       pki.HiveID
	Version      string
	ReconnectMin time.Duration // default 1s
	ReconnectMax time.Duration // default 60s
	Logger       *slog.Logger
	// OnConnect runs after every successful hello, so the hive can redo
	// what a fresh cloud may need (certificate request, state sync).
	OnConnect func()
}

// Agent keeps one connection to the gateway open, answering pings and
// commands, and reconnects with jittered exponential backoff when it drops.
type Agent struct {
	cfg     AgentConfig
	log     *slog.Logger
	handler Handler

	mu sync.Mutex
	ws *websocket.Conn // current connection, nil while disconnected
}

// ErrNotConnected is returned by Emit while the agent has no connection.
var ErrNotConnected = errors.New("tunnel: not connected")

// Emit sends an event to the cloud over the current connection.
func (a *Agent) Emit(ctx context.Context, e protocol.Event) error {
	a.mu.Lock()
	ws := a.ws
	a.mu.Unlock()
	if ws == nil {
		return ErrNotConnected
	}
	return write(ctx, ws, protocol.KindEvent, "", e)
}

// Connected reports whether a session is currently established.
func (a *Agent) Connected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ws != nil
}

// NewAgent creates an Agent; Run starts it.
func NewAgent(cfg AgentConfig, handler Handler) *Agent {
	if cfg.ReconnectMin == 0 {
		cfg.ReconnectMin = time.Second
	}
	if cfg.ReconnectMax == 0 {
		cfg.ReconnectMax = time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Agent{cfg: cfg, log: cfg.Logger.With("hive", cfg.HiveID), handler: handler}
}

// Run blocks until ctx is cancelled, reconnecting as needed.
func (a *Agent) Run(ctx context.Context) {
	backoff := a.cfg.ReconnectMin
	for {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > a.cfg.ReconnectMax {
			backoff = a.cfg.ReconnectMin // a long-lived session resets the backoff
		}
		a.log.Info("connection ended", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff = min(backoff*2, a.cfg.ReconnectMax)
	}
}

// session dials, performs hello/welcome and serves until the connection drops.
func (a *Agent) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dialCtx, a.cfg.URL, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: a.cfg.TLS}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()

	hello := protocol.Hello{HiveID: string(a.cfg.HiveID), Version: a.cfg.Version, ProtocolVersion: protocol.Version}
	if err := write(ctx, ws, protocol.KindHello, "", hello); err != nil {
		return err
	}
	env, err := read(ctx, ws)
	if err != nil {
		return err
	}
	var welcome protocol.Welcome
	if env.Kind != protocol.KindWelcome || env.Unmarshal(&welcome) != nil {
		return errUnexpected(env.Kind)
	}
	a.log.Info("connected", "server_time", welcome.ServerTime, "ping_interval", welcome.PingInterval)
	a.mu.Lock()
	a.ws = ws
	a.mu.Unlock()
	if a.cfg.OnConnect != nil {
		go a.cfg.OnConnect()
	}
	defer func() {
		a.mu.Lock()
		a.ws = nil
		a.mu.Unlock()
	}()

	for {
		env, err := read(ctx, ws)
		if err != nil {
			return err
		}
		if err := a.handle(ctx, ws, env); err != nil {
			return err
		}
	}
}

// handle answers one inbound envelope.
func (a *Agent) handle(ctx context.Context, ws *websocket.Conn, env protocol.Envelope) error {
	switch env.Kind {
	case protocol.KindPing:
		return write(ctx, ws, protocol.KindPong, env.ID, nil)
	case protocol.KindCommand:
		var cmd protocol.Command
		if err := env.Unmarshal(&cmd); err != nil {
			return write(ctx, ws, protocol.KindResult, env.ID, protocol.Result{Error: "bad command: " + err.Error()})
		}
		return write(ctx, ws, protocol.KindResult, env.ID, a.handler(ctx, cmd))
	case protocol.KindPong:
	default:
		a.log.Warn("unexpected kind from gateway", "kind", env.Kind)
	}
	return nil
}

type errUnexpected protocol.Kind

func (e errUnexpected) Error() string { return "tunnel: unexpected message kind " + string(e) }

// jitter returns d ± 20 %.
func jitter(d time.Duration) time.Duration {
	f := 0.8 + rand.Float64()*0.4 //nolint:gosec // backoff jitter, not security
	return time.Duration(float64(d) * f)
}
