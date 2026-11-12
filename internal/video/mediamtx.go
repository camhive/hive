package video

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	"github.com/camhive/hive/internal/pull"
)

// MediaMTXConfig configures the MediaMTX-backed engine.
type MediaMTXConfig struct {
	Host            string // bind/advertise address for control, playback and WebRTC; default 127.0.0.1
	APIPort         int    // MediaMTX control API; default 9997
	PlaybackPort    int    // archive playback server; default 9996
	WebRTCPort      int    // WHEP endpoint; default 8889
	RTSPPort        int    // loopback RTSP port the Hive publishes camera streams to; a free one at every Start when 0
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

// publisher is how the Hive reaches the engine's loopback RTSP server to
// publish camera streams (ADR-0015). The password is random per engine
// start, so other local users cannot publish into a camera's path.
type publisher struct {
	port       int
	user, pass string
}

// newPublisher returns credentials for one engine start, on port or, when
// port is 0, on a free loopback port.
func newPublisher(port int) (publisher, error) {
	if port == 0 {
		var err error
		if port, err = freeLoopbackPort(); err != nil {
			return publisher{}, fmt.Errorf("video: rtsp port: %w", err)
		}
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return publisher{}, err
	}
	return publisher{port: port, user: "hive", pass: hex.EncodeToString(secret)}, nil
}

// url is where the Hive publishes one engine path.
func (p publisher) url(path string) string {
	u := url.URL{Scheme: "rtsp", User: url.UserPassword(p.user, p.pass), Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)), Path: "/" + path}
	return u.String()
}

func freeLoopbackPort() (int, error) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// baseYAML is the static server configuration; paths are added at runtime
// through the API so sources can change without a restart.
func (c MediaMTXConfig) baseYAML(pub publisher) string {
	var b strings.Builder
	fmt.Fprintf(&b, "logLevel: warn\napi: yes\napiAddress: %s:%d\n", c.Host, c.APIPort)
	fmt.Fprintf(&b, "playback: yes\nplaybackAddress: %s:%d\n", c.Host, c.PlaybackPort)
	// WHEP signalling answers the Hive only (it checks access tokens and
	// forwards the offer); ICE media over UDP must reach browsers on the LAN.
	fmt.Fprintf(&b, "webrtc: yes\nwebrtcAddress: %s:%d\nwebrtcLocalUDPAddress: :%d\nwebrtcIPsFromInterfaces: yes\n", c.Host, c.WebRTCPort, c.WebRTCPort+1)
	if len(c.ICEServers) > 0 {
		b.WriteString("webrtcICEServers2:\n")
		for _, s := range c.ICEServers {
			fmt.Fprintf(&b, "  - url: %s\n", s.URL)
			if s.Username != "" {
				fmt.Fprintf(&b, "    username: %s\n    password: %s\n", s.Username, s.Password)
			}
		}
	}
	fmt.Fprintf(&b, "rtsp: yes\nrtspAddress: 127.0.0.1:%d\nrtspTransports: [tcp]\nrtspEncryption: \"no\"\n", pub.port)
	b.WriteString("rtmp: no\nhls: no\nsrt: no\nmoq: no\nmetrics: no\npprof: no\n")
	writeAuth(&b, pub)
	b.WriteString("paths: {}\n")
	return b.String()
}

// writeAuth renders MediaMTX's defaults minus publishing for anyone: anyone
// who reaches the WHEP and playback listeners (bound to Host, loopback by
// default) may read and play back, the API answers loopback only,
// and only the Hive may publish.
func writeAuth(b *strings.Builder, pub publisher) {
	const loopback = "    ips: [127.0.0.1, \"::1\"]\n"
	b.WriteString("authMethod: internal\nauthInternalUsers:\n")
	b.WriteString("  - user: any\n    pass: \"\"\n    ips: []\n    permissions:\n      - action: read\n      - action: playback\n")
	b.WriteString("  - user: any\n    pass: \"\"\n" + loopback + "    permissions:\n      - action: api\n")
	fmt.Fprintf(b, "  - user: %q\n    pass: %q\n"+loopback+"    permissions:\n      - action: publish\n", pub.user, pub.pass)
}

// pathConfig is the per-source MediaMTX path configuration: the Hive
// publishes the camera stream (ADR-0015) and the engine records fMP4
// segments under ArchiveDir. Deletion is disabled; retention belongs to the
// Hive.
func (c MediaMTXConfig) pathConfig() []byte {
	// %z carries the UTC offset: MediaMTX formats the name in local time and
	// the archive index must recover the instant exactly (ADR-0009).
	recordPath := filepath.ToSlash(filepath.Join(c.ArchiveDir, "%path", "%Y-%m-%d_%H-%M-%S-%f_%z"))
	body, _ := json.Marshal(map[string]any{
		"source":                "publisher",
		"overridePublisher":     true,
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
	pub    publisher     // this start's loopback RTSP credentials

	pullsMu sync.Mutex
	pulls   map[string]*pullHandle // by path name
}

// pullHandle is one running camera pull (internal/pull).
type pullHandle struct {
	src    Source
	cancel context.CancelFunc
	done   chan struct{}
}

func (h *pullHandle) stop() {
	h.cancel()
	<-h.done
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
	m := &MediaMTX{MediaMTXConfig: cfg, log: slog.New(slog.DiscardHandler), client: &http.Client{Timeout: 5 * time.Second}, pulls: map[string]*pullHandle{}}
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
	confPath, pub, err := m.writeConfig()
	if err != nil {
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
	m.cmd, m.cancel, m.exited, m.pub = cmd, cancel, exited, pub
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

// writeConfig prepares the work and archive directories and writes this
// start's configuration with fresh publisher credentials.
func (m *MediaMTX) writeConfig() (string, publisher, error) {
	if m.WorkDir == "" {
		dir, err := os.MkdirTemp("", "camhive-video-")
		if err != nil {
			return "", publisher{}, err
		}
		m.WorkDir = dir
	} else if err := os.MkdirAll(m.WorkDir, 0o750); err != nil {
		return "", publisher{}, fmt.Errorf("video: workdir: %w", err)
	}
	if err := os.MkdirAll(m.ArchiveDir, 0o750); err != nil {
		return "", publisher{}, fmt.Errorf("video: archive dir: %w", err)
	}
	pub, err := newPublisher(m.RTSPPort)
	if err != nil {
		return "", publisher{}, err
	}
	confPath := filepath.Join(m.WorkDir, "mediamtx.yml")
	return confPath, pub, os.WriteFile(confPath, []byte(m.baseYAML(pub)), 0o600)
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

// Stop implements Engine: camera pulls end with the process; a restarted
// engine gets its sources again through AddSource.
func (m *MediaMTX) Stop() error {
	m.stopPulls()
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

// AddSource implements Engine: add the path, or replace it when it exists,
// and pull the camera into it. A changed source (URL, audio) restarts the
// pull; the same source again leaves it running.
func (m *MediaMTX) AddSource(ctx context.Context, src Source) error {
	name := pathName(src.Camera, src.Stream)
	if err := m.putPath(ctx, name); err != nil {
		return err
	}
	m.startPull(name, src)
	return nil
}

// putPath creates or replaces an engine path that waits for a publisher.
func (m *MediaMTX) putPath(ctx context.Context, name string) error {
	body := m.pathConfig()
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

// startPull runs the camera pull for a path unless the same source is
// already being pulled.
func (m *MediaMTX) startPull(name string, src Source) {
	m.mu.Lock()
	target := m.pub.url(name)
	m.mu.Unlock()

	m.pullsMu.Lock()
	defer m.pullsMu.Unlock()
	if h, ok := m.pulls[name]; ok {
		if h.src == src {
			return
		}
		h.stop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &pullHandle{src: src, cancel: cancel, done: make(chan struct{})}
	m.pulls[name] = h
	go func() {
		defer close(h.done)
		pull.Run(ctx, pull.Config{Source: src.URL, Target: target, Audio: src.Audio,
			Logger: m.log.With("camera", src.Camera, "stream", src.Stream)})
	}()
}

// stopPull ends a path's pull; it reports whether there was one.
func (m *MediaMTX) stopPull(name string) bool {
	m.pullsMu.Lock()
	defer m.pullsMu.Unlock()
	h, ok := m.pulls[name]
	if ok {
		h.stop()
		delete(m.pulls, name)
	}
	return ok
}

func (m *MediaMTX) stopPulls() {
	m.pullsMu.Lock()
	defer m.pullsMu.Unlock()
	for name, h := range m.pulls {
		h.stop()
		delete(m.pulls, name)
	}
}

// pulling reports whether the Hive pulls a camera into the path.
func (m *MediaMTX) pulling(name string) bool {
	m.pullsMu.Lock()
	defer m.pullsMu.Unlock()
	_, ok := m.pulls[name]
	return ok
}

// RemoveSource implements Engine.
func (m *MediaMTX) RemoveSource(ctx context.Context, cam CameraID, s Stream) error {
	m.stopPull(pathName(cam, s))
	status, resp, err := m.api(ctx, http.MethodDelete, "/v3/config/paths/delete/"+pathName(cam, s), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("video: remove source: %s", resp)
	}
	return nil
}

// Status implements Engine. A source whose camera is not publishing (yet)
// has no engine path; it reports not ready, as an unreachable camera did
// when the engine pulled it itself.
func (m *MediaMTX) Status(ctx context.Context, cam CameraID, s Stream) (Status, error) {
	name := pathName(cam, s)
	status, resp, err := m.api(ctx, http.MethodGet, "/v3/paths/get/"+name, nil)
	if err != nil {
		return Status{}, err
	}
	if status == http.StatusNotFound {
		if m.pulling(name) {
			return Status{}, nil
		}
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
