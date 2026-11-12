package camsim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Simulator runs MediaMTX, one ffmpeg publisher per stream, the ONVIF mock and
// the WS-Discovery responder. Start it once per test binary; it is not safe to
// start twice.
type Simulator struct {
	cfg    Config
	log    *slog.Logger
	cancel context.CancelFunc
	procs  []*exec.Cmd
	http   *http.Server
	done   chan struct{}
	ptz    *ptzLog
}

// Option customises a Simulator.
type Option func(*Simulator)

// WithLogger sets the logger; the default discards.
func WithLogger(l *slog.Logger) Option { return func(s *Simulator) { s.log = l } }

// New validates cfg and applies defaults without starting anything.
func New(cfg Config, opts ...Option) (*Simulator, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var err error
	if cfg.MediaMTXPath, err = lookPath(cfg.MediaMTXPath, "mediamtx"); err != nil {
		return nil, err
	}
	if cfg.FFmpegPath, err = lookPath(cfg.FFmpegPath, "ffmpeg"); err != nil {
		return nil, err
	}
	s := &Simulator{cfg: cfg, log: slog.New(slog.DiscardHandler), done: make(chan struct{}), ptz: newPTZLog()}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// ErrMissingTool is returned when mediamtx or ffmpeg cannot be found.
var ErrMissingTool = errors.New("camsim: required tool not found")

func lookPath(explicit, name string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrMissingTool, explicit, err)
		}
		return explicit, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s in PATH", ErrMissingTool, name)
	}
	return p, nil
}

// Config returns the effective configuration (defaults applied).
func (s *Simulator) Config() Config { return s.cfg }

// PTZCommands returns every PTZ command the mock has received, in order.
func (s *Simulator) PTZCommands() []PTZCommand { return s.ptz.Commands() }

// Start launches all processes and blocks until every stream is being
// published or ctx expires. Stop must be called afterwards even on error.
func (s *Simulator) Start(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			s.Stop()
		}
	}()
	if s.cfg.WorkDir == "" {
		if s.cfg.WorkDir, err = os.MkdirTemp("", "camsim-"); err != nil {
			return err
		}
	} else if err = os.MkdirAll(s.cfg.WorkDir, 0o750); err != nil {
		return fmt.Errorf("camsim: workdir: %w", err)
	}
	confPath := filepath.Join(s.cfg.WorkDir, "mediamtx.yml")
	if err = os.WriteFile(confPath, []byte(s.cfg.mediamtxYAML()), 0o600); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	if err = s.spawn(runCtx, "mediamtx", s.cfg.MediaMTXPath, confPath); err != nil {
		return err
	}
	if err = waitTCP(ctx, net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.RTSPPort))); err != nil {
		return fmt.Errorf("camsim: mediamtx did not open RTSP port: %w", err)
	}

	if err = s.startPublishers(runCtx); err != nil {
		return err
	}
	if err = s.startONVIF(ctx, runCtx); err != nil {
		return err
	}
	return s.waitPublishing(ctx)
}

// startPublishers launches one ffmpeg per stream.
func (s *Simulator) startPublishers(ctx context.Context) error {
	for _, cam := range s.cfg.Cameras {
		for _, st := range []Stream{Main, Sub} {
			name := "ffmpeg-" + cam.Name + "-" + string(st)
			if err := s.spawn(ctx, name, s.cfg.FFmpegPath, s.cfg.ffmpegArgs(cam, st)...); err != nil {
				return err
			}
		}
	}
	return nil
}

// startONVIF serves the ONVIF mock and, when the network allows, WS-Discovery.
func (s *Simulator) startONVIF(ctx, runCtx context.Context) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.ONVIFPort)))
	if err != nil {
		return fmt.Errorf("camsim: onvif listen: %w", err)
	}
	s.http = &http.Server{Handler: newONVIFHandlerWithPTZ(&s.cfg, s.ptz), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(ln) }()

	if _, err = startDiscovery(runCtx, &s.cfg, wsDiscoveryMulticast); err != nil {
		// Multicast may be unavailable (CI sandboxes); the mock is still usable
		// by direct address, so log and continue.
		s.log.Warn("ws-discovery disabled", "err", err)
	}
	return nil
}

func (s *Simulator) spawn(ctx context.Context, name, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)                        //nolint:gosec // binary and args come from validated Config
	logFile, err := os.Create(filepath.Join(s.cfg.WorkDir, name+".log")) //nolint:gosec // WorkDir is ours; name is internal
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Dir = s.cfg.WorkDir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("camsim: start %s: %w", name, err)
	}
	s.log.Info("started", "proc", name, "pid", cmd.Process.Pid)
	s.procs = append(s.procs, cmd)
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		s.log.Info("exited", "proc", name, "err", err)
	}()
	return nil
}

// waitPublishing polls the MediaMTX API until every path has a publisher.
func (s *Simulator) waitPublishing(ctx context.Context) error {
	api := fmt.Sprintf("http://%s:%d/v3/paths/get/", s.cfg.Host, s.cfg.APIPort)
	client := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		pending := 0
		for _, cam := range s.cfg.Cameras {
			for _, st := range []Stream{Main, Sub} {
				if !pathReady(ctx, client, api+cam.StreamPath(st)) {
					pending++
				}
			}
		}
		if pending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("camsim: %d stream(s) not publishing: %w", pending, ctx.Err())
		case <-tick.C:
		}
	}
}

func pathReady(ctx context.Context, c *http.Client, url string) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var path struct {
		Ready bool `json:"ready"`
	}
	return json.Unmarshal(body, &path) == nil && path.Ready
}

func waitTCP(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	for {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Stop terminates all processes and servers. Safe to call more than once.
func (s *Simulator) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.http.Shutdown(ctx)
		cancel()
		s.http = nil
	}
	for _, p := range s.procs {
		if p.Process != nil {
			_ = p.Process.Kill()
		}
	}
	s.procs = nil
}
