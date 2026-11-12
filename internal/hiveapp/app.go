// Package hiveapp is the long-running body of the hive service: it owns the
// lifecycle (start, serve, graceful stop) that the OS service wrapper in
// cmd/hive drives. Stage 0: a local health endpoint; the video engine,
// tunnel agent and local API attach here in stage 1 and 2.
package hiveapp

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Config configures the hive.
type Config struct {
	ListenAddr string // local API; default 127.0.0.1:16016
	// TLSListenAddr and TLS add a second listener serving the same API
	// over TLS with the hive's public certificate (ADR-0012): the relay
	// pipes clients into it and, with #32, the LAN interface uses it too.
	TLSListenAddr string
	TLS           *tls.Config
	// Relay, when set with TLS, serves the API over TLS to streams the cloud
	// relay pipes in; requests on it count as remote (see Relayed).
	Relay   *RelayListener
	Version string
	Logger  *slog.Logger
	Handler http.Handler // the API; default NewAPI with no store (health only)
}

// ApplyDefaults fills zero values. The local API binds loopback only; remote
// access goes through tokens and the tunnel, never through an open port.
func (c *Config) ApplyDefaults() {
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:16016"
	}
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.Handler == nil {
		c.Handler = NewAPI(Deps{Version: c.Version, Logger: c.Logger})
	}
}

// App is one hive instance.
type App struct {
	cfg Config
}

// New creates an App with defaults applied.
func New(cfg Config) *App {
	cfg.ApplyDefaults()
	return &App{cfg: cfg}
}

// Run serves until ctx is cancelled, then shuts down gracefully. It returns
// nil on a clean stop and an error if it could not start or a server failed.
func (a *App) Run(ctx context.Context) error {
	servers, err := a.listen(ctx)
	if err != nil {
		return err
	}
	errCh := make(chan error, len(servers)) // every server can report once without blocking
	for _, s := range servers {
		go func() { errCh <- s.serve() }()
	}
	a.cfg.Logger.Info("hive started", "addr", servers[0].ln.Addr().String(), "version", a.cfg.Version)

	select {
	case <-ctx.Done():
		shutdown(servers)
		a.cfg.Logger.Info("hive stopped")
		return nil
	case err := <-errCh:
		shutdown(servers)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// served is one listener with the server answering on it.
type served struct {
	srv *http.Server
	ln  net.Listener
}

func (s served) serve() error {
	if s.srv.TLSConfig != nil {
		return s.srv.ServeTLS(s.ln, "", "")
	}
	return s.srv.Serve(s.ln)
}

// listen opens the plain API on loopback, the TLS API for the LAN and the
// relay's listener, as configured. Nothing stays open on error.
func (a *App) listen(ctx context.Context) ([]served, error) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", a.cfg.ListenAddr)
	if err != nil {
		return nil, err
	}
	out := []served{{a.server(nil, nil), ln}}
	if a.cfg.TLS == nil {
		return out, nil
	}
	if a.cfg.TLSListenAddr != "" {
		tlsLn, err := lc.Listen(ctx, "tcp", a.cfg.TLSListenAddr)
		if err != nil {
			_ = ln.Close()
			return nil, err
		}
		out = append(out, served{a.server(a.cfg.TLS, nil), tlsLn})
		a.cfg.Logger.Info("tls api listening", "addr", tlsLn.Addr().String())
	}
	if a.cfg.Relay != nil {
		out = append(out, served{a.server(a.cfg.TLS, RelayedConn), a.cfg.Relay})
	}
	return out, nil
}

func (a *App) server(tlsCfg *tls.Config, connCtx func(context.Context, net.Conn) context.Context) *http.Server {
	return &http.Server{Handler: a.cfg.Handler, ReadHeaderTimeout: 5 * time.Second, TLSConfig: tlsCfg, ConnContext: connCtx}
}

func shutdown(servers []served) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.srv.Shutdown(ctx)
	}
}
