// Package tunnel is the persistent, mutually authenticated channel between a
// Hive and the cloud (ADR-0004). The Hive dials out over TLS 1.3 with its
// device certificate and keeps a WebSocket open; the cloud Gateway accepts
// connections, identifies the Hive by its certificate and exchanges
// protocol envelopes: hello/welcome, ping/pong, command/result, event.
package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
)

// Errors.
var (
	ErrHiveOffline = errors.New("tunnel: hive is not connected")
	ErrClosed      = errors.New("tunnel: gateway closed")
)

// Hooks let the owner of a Gateway gate connections and observe them
// without the tunnel knowing about databases or message buses. All are
// optional; OnHello returning an error rejects the connection.
type Hooks struct {
	OnHello   func(id pki.HiveID, hello protocol.Hello) error
	OnOnline  func(id pki.HiveID)
	OnOffline func(id pki.HiveID)
	OnEvent   func(id pki.HiveID, event protocol.Event)
}

// GatewayConfig configures a Gateway.
type GatewayConfig struct {
	PingInterval time.Duration // default 15s; a hive missing two pings is dropped
	Logger       *slog.Logger
	Hooks        Hooks
}

// Gateway terminates hive connections and routes commands to them.
type Gateway struct {
	cfg    GatewayConfig
	log    *slog.Logger
	server *http.Server
	mux    *http.ServeMux

	mu    sync.RWMutex
	hives map[pki.HiveID]*hiveConn
	seq   atomic.Uint64
}

// NewGateway creates a Gateway; call Serve with a TLS listener built from
// pki.CA.ServerTLSConfig so that every connection carries a verified device
// certificate.
func NewGateway(cfg GatewayConfig) *Gateway {
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 15 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	g := &Gateway{cfg: cfg, log: cfg.Logger, hives: make(map[pki.HiveID]*hiveConn)}
	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET /v1/hive", g.handleHive)
	g.server = &http.Server{Handler: g.mux, ReadHeaderTimeout: 10 * time.Second}
	return g
}

// Handle mounts an extra handler on the gateway's mTLS server (for example
// certificate renewal). Call it before Serve. Handlers can trust
// r.TLS.PeerCertificates[0]: the listener verified it against the Device CA.
func (g *Gateway) Handle(pattern string, h http.Handler) { g.mux.Handle(pattern, h) }

// Serve accepts connections until the listener is closed.
func (g *Gateway) Serve(ln net.Listener) error {
	err := g.server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops accepting and drops every hive connection.
func (g *Gateway) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	g.mu.Lock()
	for id, hc := range g.hives {
		hc.close(websocket.StatusGoingAway, "gateway closing")
		delete(g.hives, id)
	}
	g.mu.Unlock()
	return g.server.Shutdown(ctx)
}

// Online reports whether a hive currently holds a connection.
func (g *Gateway) Online(id pki.HiveID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.hives[id]
	return ok
}

// Send delivers a command to a hive and waits for its result.
func (g *Gateway) Send(ctx context.Context, id pki.HiveID, cmd protocol.Command) (protocol.Result, error) {
	g.mu.RLock()
	hc, ok := g.hives[id]
	g.mu.RUnlock()
	if !ok {
		return protocol.Result{}, fmt.Errorf("%w: %s", ErrHiveOffline, id)
	}
	return hc.send(ctx, "c-"+strconv.FormatUint(g.seq.Add(1), 10), cmd)
}

// handleHive upgrades the connection, validates hello against the
// certificate, registers the hive and pumps messages until it drops.
func (g *Gateway) handleHive(w http.ResponseWriter, r *http.Request) {
	id, ok := hiveIDFromTLS(r.TLS)
	if !ok {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	hello, err := readHello(ctx, ws)
	if err != nil || pki.HiveID(hello.HiveID) != id {
		g.log.Warn("hello rejected", "cert_id", id, "hello_id", hello.HiveID, "err", err)
		_ = ws.Close(websocket.StatusPolicyViolation, "hello does not match certificate")
		return
	}
	if hello.ProtocolVersion < protocol.Version {
		_ = ws.Close(websocket.StatusPolicyViolation, "protocol too old")
		return
	}
	if g.cfg.Hooks.OnHello != nil {
		if err := g.cfg.Hooks.OnHello(id, hello); err != nil {
			g.log.Warn("hello refused by hook", "hive", id, "err", err)
			_ = ws.Close(websocket.StatusPolicyViolation, "refused: "+err.Error())
			return
		}
	}
	welcome := protocol.Welcome{ServerTime: time.Now().UTC(), PingInterval: g.cfg.PingInterval, MinProtocolVersion: protocol.Version}
	if err := write(ctx, ws, protocol.KindWelcome, "", welcome); err != nil {
		return
	}

	hc := newHiveConn(ws, g.log.With("hive", id))
	hc.onEvent = func(e protocol.Event) {
		if g.cfg.Hooks.OnEvent != nil {
			g.cfg.Hooks.OnEvent(id, e)
		}
	}
	g.register(id, hc)
	defer g.unregister(id, hc)
	g.log.Info("hive online", "hive", id, "version", hello.Version)
	if g.cfg.Hooks.OnOnline != nil {
		g.cfg.Hooks.OnOnline(id)
	}

	go hc.pingLoop(ctx, g.cfg.PingInterval)
	hc.readLoop(ctx) // returns when the connection drops
	g.log.Info("hive offline", "hive", id)
	if g.cfg.Hooks.OnOffline != nil {
		g.cfg.Hooks.OnOffline(id)
	}
}

func (g *Gateway) register(id pki.HiveID, hc *hiveConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if old, ok := g.hives[id]; ok {
		// A second connection for the same id: newest wins; the old one is
		// closed so clones surface as flapping (ADR-0006).
		old.close(websocket.StatusPolicyViolation, "superseded by a newer connection")
	}
	g.hives[id] = hc
}

func (g *Gateway) unregister(id pki.HiveID, hc *hiveConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hives[id] == hc {
		delete(g.hives, id)
	}
	hc.failPending(ErrHiveOffline)
}

func hiveIDFromTLS(cs *tls.ConnectionState) (pki.HiveID, bool) {
	if cs == nil || len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return "", false
	}
	id := pki.HiveIDFromCertificate(cs.VerifiedChains[0][0])
	return id, id.Valid()
}

func readHello(ctx context.Context, ws *websocket.Conn) (protocol.Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	env, err := read(ctx, ws)
	if err != nil {
		return protocol.Hello{}, err
	}
	if env.Kind != protocol.KindHello {
		return protocol.Hello{}, fmt.Errorf("tunnel: expected hello, got %s", env.Kind)
	}
	var h protocol.Hello
	return h, env.Unmarshal(&h)
}

// hiveConn is one connected hive with its in-flight commands.
type hiveConn struct {
	ws  *websocket.Conn
	log *slog.Logger

	mu      sync.Mutex
	pending map[string]chan protocol.Result
	lastRx  atomic.Int64 // unix nanos of last message received
	onEvent func(protocol.Event)
}

func newHiveConn(ws *websocket.Conn, log *slog.Logger) *hiveConn {
	hc := &hiveConn{ws: ws, log: log, pending: make(map[string]chan protocol.Result)}
	hc.lastRx.Store(time.Now().UnixNano())
	return hc
}

func (hc *hiveConn) send(ctx context.Context, id string, cmd protocol.Command) (protocol.Result, error) {
	ch := make(chan protocol.Result, 1)
	hc.mu.Lock()
	hc.pending[id] = ch
	hc.mu.Unlock()
	defer func() {
		hc.mu.Lock()
		delete(hc.pending, id)
		hc.mu.Unlock()
	}()
	if err := write(ctx, hc.ws, protocol.KindCommand, id, cmd); err != nil {
		return protocol.Result{}, fmt.Errorf("%w: %w", ErrHiveOffline, err)
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return protocol.Result{}, ErrHiveOffline
		}
		return res, nil
	case <-ctx.Done():
		return protocol.Result{}, ctx.Err()
	}
}

func (hc *hiveConn) readLoop(ctx context.Context) {
	for {
		env, err := read(ctx, hc.ws)
		if err != nil {
			return
		}
		hc.lastRx.Store(time.Now().UnixNano())
		hc.dispatch(ctx, env)
	}
}

func (hc *hiveConn) dispatch(ctx context.Context, env protocol.Envelope) {
	switch env.Kind {
	case protocol.KindResult:
		var res protocol.Result
		if err := env.Unmarshal(&res); err != nil {
			hc.log.Warn("bad result", "err", err)
			return
		}
		hc.mu.Lock()
		ch, ok := hc.pending[env.ID]
		hc.mu.Unlock()
		if ok {
			ch <- res
		}
	case protocol.KindPing:
		_ = write(ctx, hc.ws, protocol.KindPong, env.ID, nil)
	case protocol.KindPong:
	case protocol.KindEvent:
		var e protocol.Event
		if err := env.Unmarshal(&e); err != nil {
			hc.log.Warn("bad event", "err", err)
			return
		}
		if hc.onEvent != nil {
			hc.onEvent(e)
		}
	default:
		hc.log.Warn("unexpected kind from hive", "kind", env.Kind)
	}
}

// pingLoop keeps the connection alive and drops hives that stop answering.
func (hc *hiveConn) pingLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(0, hc.lastRx.Load())) > 2*interval {
				hc.close(websocket.StatusGoingAway, "ping timeout")
				return
			}
			if err := write(ctx, hc.ws, protocol.KindPing, "", nil); err != nil {
				return
			}
		}
	}
}

func (hc *hiveConn) failPending(err error) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for id, ch := range hc.pending {
		close(ch)
		delete(hc.pending, id)
	}
	_ = err
}

func (hc *hiveConn) close(code websocket.StatusCode, reason string) {
	_ = hc.ws.Close(code, reason)
}

// read and write move one envelope over a websocket.
func read(ctx context.Context, ws *websocket.Conn) (protocol.Envelope, error) {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.Decode(data)
}

func write(ctx context.Context, ws *websocket.Conn, kind protocol.Kind, id string, payload any) error {
	env, err := protocol.NewEnvelope(kind, id, payload)
	if err != nil {
		return err
	}
	raw, err := protocol.Encode(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, raw)
}
