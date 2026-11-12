package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaMTXConfig configures the MediaMTX-backed engine.
type MediaMTXConfig struct {
	Host            string // bind/advertise address for control, playback and WebRTC; default 127.0.0.1
	APIPort         int    // MediaMTX control API; default 9997
	PlaybackPort    int    // archive playback server; default 9996
	WebRTCPort      int    // WHEP endpoint; default 8889
	ArchiveDir      string // where fMP4 segments are written (required)
	SegmentDuration time.Duration
	WorkDir         string // generated config and logs; temp dir when empty
	Binary          string // mediamtx executable; looked up in PATH when empty
	ICEServers      []ICEServer
}

// ICEServer is a STUN or TURN server handed to WebRTC peers so that live
// video reaches viewers behind NAT; TURN relays when direct paths fail.
type ICEServer struct {
	URL      string // stun:host:port, turn:host:port[?transport=udp|tcp], turns:...
	Username string
	Password string
}

// ErrMissingTool is returned when the mediamtx binary cannot be found.
var ErrMissingTool = errors.New("video: mediamtx binary not found")

func (c *MediaMTXConfig) applyDefaults() {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.APIPort == 0 {
		c.APIPort = 9997
	}
	if c.PlaybackPort == 0 {
		c.PlaybackPort = 9996
	}
	if c.WebRTCPort == 0 {
		c.WebRTCPort = 8889
	}
	if c.SegmentDuration == 0 {
		c.SegmentDuration = 10 * time.Minute
	}
}

func (c MediaMTXConfig) validate() error {
	if c.ArchiveDir == "" {
		return errors.New("video: ArchiveDir is required")
	}
	return nil
}

// pathName maps a camera stream to a MediaMTX path.
func pathName(cam CameraID, s Stream) string {
	return string(cam) + "/" + string(s)
}

// baseYAML is the static server configuration; paths are added at runtime
// through the API so sources can change without a restart.
func (c MediaMTXConfig) baseYAML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "logLevel: warn\napi: yes\napiAddress: %s:%d\n", c.Host, c.APIPort)
	fmt.Fprintf(&b, "playback: yes\nplaybackAddress: %s:%d\n", c.Host, c.PlaybackPort)
	fmt.Fprintf(&b, "webrtc: yes\nwebrtcAddress: :%d\nwebrtcLocalUDPAddress: :%d\nwebrtcIPsFromInterfaces: yes\n", c.WebRTCPort, c.WebRTCPort+1)
	if len(c.ICEServers) > 0 {
		b.WriteString("webrtcICEServers2:\n")
		for _, s := range c.ICEServers {
			fmt.Fprintf(&b, "  - url: %s\n", s.URL)
			if s.Username != "" {
				fmt.Fprintf(&b, "    username: %s\n    password: %s\n", s.Username, s.Password)
			}
		}
	}
	b.WriteString("rtsp: no\nrtmp: no\nhls: no\nsrt: no\nmoq: no\nmetrics: no\npprof: no\n")
	b.WriteString("paths: {}\n")
	return b.String()
}

// pathConfig is the per-source MediaMTX path configuration: pull the RTSP
// source permanently and record fMP4 segments under ArchiveDir. Deletion is
// disabled; retention belongs to the Hive.
func (c MediaMTXConfig) pathConfig(src Source) []byte {
	// %z carries the UTC offset: MediaMTX formats the name in local time and
	// the archive index must recover the instant exactly (ADR-0009).
	recordPath := filepath.ToSlash(filepath.Join(c.ArchiveDir, "%path", "%Y-%m-%d_%H-%M-%S-%f_%z"))
	body, _ := json.Marshal(map[string]any{
		"source":                src.URL,
		"sourceOnDemand":        false,
		"record":                true,
		"recordPath":            recordPath,
		"recordFormat":          "fmp4",
		"recordPartDuration":    "1s",
		"recordSegmentDuration": c.SegmentDuration.String(),
		"recordDeleteAfter":     "0s",
	})
	return body
}

// PlaybackURL implements Engine.
func (c MediaMTXConfig) PlaybackURL(cam CameraID, s Stream, start time.Time, d time.Duration) string {
	q := url.Values{}
	q.Set("path", pathName(cam, s))
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("duration", strconv.FormatFloat(d.Seconds(), 'f', -1, 64))
	q.Set("format", "fmp4")
	return fmt.Sprintf("http://%s:%d/get?%s", c.Host, c.PlaybackPort, q.Encode())
}

// WHEPURL implements Engine.
func (c MediaMTXConfig) WHEPURL(cam CameraID, s Stream) string {
	return fmt.Sprintf("http://%s:%d/%s/whep", c.Host, c.WebRTCPort, pathName(cam, s))
}

func parseStatus(raw []byte) (Status, error) {
	var p struct {
		Ready         bool     `json:"ready"`
		Tracks        []string `json:"tracks"`
		Readers       []any    `json:"readers"`
		BytesReceived int64    `json:"bytesReceived"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Status{}, fmt.Errorf("video: parse path status: %w", err)
	}
	return Status{Ready: p.Ready, Tracks: p.Tracks, Readers: len(p.Readers), BytesReceived: p.BytesReceived}, nil
}

func parseSegments(raw []byte) ([]Segment, error) {
	var p struct {
		Segments []struct {
			Start    time.Time `json:"start"`
			Duration *float64  `json:"duration"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("video: parse recordings: %w", err)
	}
	segs := make([]Segment, 0, len(p.Segments))
	for _, s := range p.Segments {
		seg := Segment{Start: s.Start}
		if s.Duration != nil {
			seg.Duration = time.Duration(*s.Duration * float64(time.Second))
		}
		segs = append(segs, seg)
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start.Before(segs[j].Start) })
	return segs, nil
}

func isAlreadyExists(body []byte) bool {
	return bytes.Contains(body, []byte("already exists"))
}

// MediaMTX runs MediaMTX as a supervised sidecar and implements Engine.
type MediaMTX struct {
	MediaMTXConfig
	log    *slog.Logger
	client *http.Client

	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	exited chan struct{} // closed when the current process has exited
}

// Option customises a MediaMTX engine.
type Option func(*MediaMTX)

// WithLogger sets the logger; the default discards.
func WithLogger(l *slog.Logger) Option { return func(m *MediaMTX) { m.log = l } }

// NewMediaMTX validates cfg, applies defaults and locates the binary.
func NewMediaMTX(cfg MediaMTXConfig, opts ...Option) (*MediaMTX, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Binary == "" {
		p, err := exec.LookPath("mediamtx")
		if err != nil {
			return nil, fmt.Errorf("%w: not in PATH", ErrMissingTool)
		}
		cfg.Binary = p
	} else if _, err := os.Stat(cfg.Binary); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrMissingTool, cfg.Binary, err)
	}
	m := &MediaMTX{MediaMTXConfig: cfg, log: slog.New(slog.DiscardHandler), client: &http.Client{Timeout: 5 * time.Second}}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// Start implements Engine.
func (m *MediaMTX) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return nil
	}
	if m.WorkDir == "" {
		dir, err := os.MkdirTemp("", "camhive-video-")
		if err != nil {
			return err
		}
		m.WorkDir = dir
	} else if err := os.MkdirAll(m.WorkDir, 0o750); err != nil {
		return fmt.Errorf("video: workdir: %w", err)
	}
	if err := os.MkdirAll(m.ArchiveDir, 0o750); err != nil {
		return fmt.Errorf("video: archive dir: %w", err)
	}
	confPath := filepath.Join(m.WorkDir, "mediamtx.yml")
	if err := os.WriteFile(confPath, []byte(m.baseYAML()), 0o600); err != nil {
		return err
	}
	logFile, err := os.Create(filepath.Join(m.WorkDir, "mediamtx.log")) //nolint:gosec // WorkDir is ours
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, m.Binary, confPath) //nolint:gosec // binary from validated config
	cmd.Dir = m.WorkDir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		return fmt.Errorf("video: start mediamtx: %w", err)
	}
	exited := make(chan struct{})
	m.cmd, m.cancel, m.exited = cmd, cancel, exited
	m.log.Info("mediamtx started", "pid", cmd.Process.Pid, "config", confPath)
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		m.log.Info("mediamtx exited", "err", err)
		close(exited)
	}()

	if err := waitTCP(ctx, net.JoinHostPort(m.Host, strconv.Itoa(m.APIPort))); err != nil {
		return fmt.Errorf("video: mediamtx API did not come up: %w", err)
	}
	return nil
}

// Exited returns a channel closed when the current MediaMTX process exits
// (ingest.Supervised). Before Start it returns a closed channel.
func (m *MediaMTX) Exited() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exited == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return m.exited
}

// PID returns the MediaMTX process id, or 0 when not running.
func (m *MediaMTX) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}

// Stop implements Engine.
func (m *MediaMTX) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return nil
	}
	m.cancel()
	if m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	m.cmd, m.cancel = nil, nil
	return nil
}

// AddSource implements Engine: add, or replace when the path already exists.
func (m *MediaMTX) AddSource(ctx context.Context, src Source) error {
	body := m.pathConfig(src)
	name := pathName(src.Camera, src.Stream)
	status, resp, err := m.api(ctx, http.MethodPost, "/v3/config/paths/add/"+name, body)
	if err != nil {
		return err
	}
	if status == http.StatusBadRequest && isAlreadyExists(resp) {
		status, resp, err = m.api(ctx, http.MethodPost, "/v3/config/paths/replace/"+name, body)
		if err != nil {
			return err
		}
	}
	if status != http.StatusOK {
		return fmt.Errorf("video: add source %s: %s", name, resp)
	}
	return nil
}

// RemoveSource implements Engine.
func (m *MediaMTX) RemoveSource(ctx context.Context, cam CameraID, s Stream) error {
	status, resp, err := m.api(ctx, http.MethodDelete, "/v3/config/paths/delete/"+pathName(cam, s), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("video: remove source: %s", resp)
	}
	return nil
}

// Status implements Engine.
func (m *MediaMTX) Status(ctx context.Context, cam CameraID, s Stream) (Status, error) {
	status, resp, err := m.api(ctx, http.MethodGet, "/v3/paths/get/"+pathName(cam, s), nil)
	if err != nil {
		return Status{}, err
	}
	if status == http.StatusNotFound {
		return Status{}, ErrNotFound
	}
	if status != http.StatusOK {
		return Status{}, fmt.Errorf("video: status: %s", resp)
	}
	return parseStatus(resp)
}

// Segments implements Engine.
func (m *MediaMTX) Segments(ctx context.Context, cam CameraID, s Stream) ([]Segment, error) {
	status, resp, err := m.api(ctx, http.MethodGet, "/v3/recordings/get/"+pathName(cam, s), nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("video: segments: %s", resp)
	}
	return parseSegments(resp)
}

// api performs one control-API call and returns status code and body.
// iceEntry is MediaMTX's webrtcICEServers2 item.
type iceEntry struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// SetICEServers replaces the ICE servers the running engine advertises to
// WebRTC viewers (STUN/TURN) without a restart; it implements ICEUpdater.
func (m *MediaMTX) SetICEServers(ctx context.Context, servers []ICEServer) error {
	list := make([]iceEntry, 0, len(servers))
	for _, s := range servers {
		list = append(list, iceEntry(s))
	}
	body, _ := json.Marshal(map[string]any{"webrtcICEServers2": list})
	status, out, err := m.api(ctx, http.MethodPatch, "/v3/config/global/patch", body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("video: set ICE servers: %d: %s", status, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *MediaMTX) api(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	m.mu.Lock()
	running := m.cmd != nil
	m.mu.Unlock()
	if !running {
		return 0, nil, ErrNotReady
	}
	u := fmt.Sprintf("http://%s:%d%s", m.Host, m.APIPort, path)
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("video: mediamtx api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
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

// Compile-time check.
var _ Engine = (*MediaMTX)(nil)
