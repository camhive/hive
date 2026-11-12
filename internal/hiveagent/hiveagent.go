// Package hiveagent is the hive's side of the cloud channel above the
// transport (ADR-0008): it answers the cloud's commands against the local
// store and services, sends a heartbeat with a health summary and forwards
// local events (camera online/offline, storage low) to the cloud. The
// transport (mTLS WebSocket, reconnects) is internal/hivelink.
package hiveagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

func cameraID(id string) camera.ID { return camera.ID(id) }

// HealthSource is the part of health.Monitor the agent summarises.
type HealthSource interface {
	All() []health.CameraHealth
}

// Deps are the local services commands act on. Nil Store or Health makes
// the related commands answer not_ready instead of failing.
type Deps struct {
	Store    *store.Store
	Verifier *auth.Verifier // receives pushed keys and session revocations
	Health   HealthSource
	Notify   func() // after a camera change (ingest.Notify)
	Version  string
	Protocol int
	// Free returns free bytes on the archive volume (optional).
	Free func() (int64, error)
	// Diag builds a diagnostic bundle and returns its path and entries (optional).
	Diag func(ctx context.Context) (path string, entries []string, err error)
	// License holds the cloud-issued license (nil → license.set not_ready).
	License *Licensing
	// Engine answers WHEP offers for live viewing (nil → signal.* not_ready).
	Engine video.Engine
	HTTP   *http.Client // for the engine's WHEP endpoint; default 15 s timeout
	Logger *slog.Logger
	Now    func() time.Time
}

// Agent routes commands and produces events.
type Agent struct {
	d       Deps
	log     *slog.Logger
	started time.Time
	binHash string

	mu     sync.Mutex
	queue  []protocol.Event // events waiting for a connection
	notify chan struct{}
	relay  *Relay   // tunnel.open target; nil until SetRelay
	tls    *TLSInfo // LAN TLS listener; nil until SetTLS
}

// MaxQueuedEvents bounds the offline event buffer; oldest are dropped.
const MaxQueuedEvents = 1000

// New creates an Agent.
func New(d Deps) *Agent {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Notify == nil {
		d.Notify = func() {}
	}
	if d.Protocol == 0 {
		d.Protocol = protocol.Version
	}
	return &Agent{d: d, log: d.Logger, started: d.Now(), binHash: ExecutableHash(), notify: make(chan struct{}, 1)}
}

// SetEngine attaches the video engine once it runs (signal.* commands).
func (a *Agent) SetEngine(e video.Engine) {
	a.mu.Lock()
	a.d.Engine = e
	a.mu.Unlock()
	a.applyStoredICE(context.Background())
}

// SetHealth attaches the health source once the monitor exists.
func (a *Agent) SetHealth(h HealthSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.d.Health = h
}

func (a *Agent) healthSource() HealthSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.d.Health
}

// ExecutableHash is the SHA-256 of the running binary (clone detection, #40);
// empty when it cannot be read.
func ExecutableHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe) //nolint:gosec // our own executable
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Handle answers one command; it is the tunnel.Handler.
func (a *Agent) Handle(ctx context.Context, cmd protocol.Command) protocol.Result {
	h, ok := handlers[cmd.Name]
	if !ok {
		return protocol.Fail(protocol.ErrCodeUnsupported, "unknown command "+cmd.Name)
	}
	res := h(a, ctx, cmd.Args)
	if !res.OK {
		a.log.Warn("command failed", "command", cmd.Name, "code", res.Error, "message", res.Message)
	}
	return res
}

type handler func(a *Agent, ctx context.Context, args json.RawMessage) protocol.Result

var handlers = map[string]handler{
	"ping":             (*Agent).ping,
	"camera.list":      (*Agent).cameraList,
	"camera.add":       (*Agent).cameraAdd,
	"camera.update":    (*Agent).cameraUpdate,
	"camera.remove":    (*Agent).cameraRemove,
	"keys.set":         (*Agent).keysSet,
	"grants.set":       (*Agent).grantsSet,
	"license.set":      (*Agent).licenseSet,
	"ice.set":          (*Agent).iceSet,
	"cert.set":         (*Agent).certSet,
	"session.revoke":   (*Agent).sessionRevoke,
	"diag.bundle":      (*Agent).diagBundle,
	"signal.offer":     (*Agent).signalOffer,
	"signal.candidate": (*Agent).signalCandidate,
	"signal.close":     (*Agent).signalClose,
	"tunnel.open":      (*Agent).tunnelOpen,
}

// Commands lists what this hive version answers (for hello/diagnostics).
func Commands() []string {
	out := make([]string, 0, len(handlers))
	for k := range handlers {
		out = append(out, k)
	}
	return out
}

func decodeArgs(args json.RawMessage, v any) *protocol.Result {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		r := protocol.Fail(protocol.ErrCodeBadArgs, err.Error())
		return &r
	}
	return nil
}

func (a *Agent) record(ctx context.Context, action, target string, details any) {
	if a.d.Store == nil {
		return
	}
	raw, _ := json.Marshal(details)
	if err := a.d.Store.Actions().Append(ctx, store.Action{Actor: "cloud", Action: action, Target: target, Details: raw}); err != nil {
		a.log.Error("action log", "err", err)
	}
}

func (a *Agent) ping(context.Context, json.RawMessage) protocol.Result {
	return protocol.Succeed(map[string]any{"time": a.d.Now().UTC(), "version": a.d.Version})
}

// Camera views mirror the local API (api/hive-openapi.yaml) minus
// passwords, which never leave the hive.
type cameraView struct {
	ID        string                       `json:"id"`
	Name      string                       `json:"name"`
	Group     string                       `json:"group"`
	Source    sourceView                   `json:"source"`
	Username  string                       `json:"username,omitempty"`
	Streams   map[string]camera.StreamSpec `json:"streams"`
	Enabled   bool                         `json:"enabled"`
	CreatedAt time.Time                    `json:"created_at"`
	UpdatedAt time.Time                    `json:"updated_at"`
}

type sourceView struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

type credentialsArg struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func view(c camera.Camera) cameraView {
	v := cameraView{ID: string(c.ID), Name: c.Name, Group: c.Group, Source: sourceView{Kind: string(c.Source.Kind), Address: c.Source.Address},
		Username: c.Credentials.Username, Streams: map[string]camera.StreamSpec{}, Enabled: c.Enabled, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	for k, s := range c.Streams {
		v.Streams[string(k)] = s
	}
	return v
}

func (a *Agent) requireStore() *protocol.Result {
	if a.d.Store == nil {
		r := protocol.Fail(protocol.ErrCodeNotReady, "store not open")
		return &r
	}
	return nil
}

func (a *Agent) cameraList(ctx context.Context, _ json.RawMessage) protocol.Result {
	if r := a.requireStore(); r != nil {
		return *r
	}
	cams, err := a.d.Store.Cameras().List(ctx)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	out := make([]cameraView, 0, len(cams))
	for _, c := range cams {
		out = append(out, view(c))
	}
	return protocol.Succeed(out)
}

func (a *Agent) cameraAdd(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireStore(); r != nil {
		return *r
	}
	var in struct {
		Name        string          `json:"name"`
		Group       string          `json:"group"`
		Source      sourceView      `json:"source"`
		Credentials *credentialsArg `json:"credentials"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	spec := camera.Spec{Name: in.Name, Group: in.Group, Source: camera.Source{Kind: camera.SourceKind(in.Source.Kind), Address: in.Source.Address}}
	if in.Credentials != nil {
		spec.Credentials = camera.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	c, err := camera.New(spec, a.d.Now())
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInvalid, err.Error())
	}
	if err := a.d.Store.Cameras().Put(ctx, c); err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	a.record(ctx, "camera.add", string(c.ID), map[string]any{"name": c.Name, "kind": c.Source.Kind, "address": c.Source.Address})
	a.d.Notify()
	return protocol.Succeed(view(c))
}

func (a *Agent) cameraUpdate(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireStore(); r != nil {
		return *r
	}
	var in struct {
		ID          string          `json:"id"`
		Name        *string         `json:"name"`
		Group       *string         `json:"group"`
		Enabled     *bool           `json:"enabled"`
		Credentials *credentialsArg `json:"credentials"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	c, err := a.d.Store.Cameras().Get(ctx, camera.ID(in.ID))
	if r := storeErr(err); r != nil {
		return *r
	}
	patch := camera.Patch{Name: in.Name, Group: in.Group, Enabled: in.Enabled}
	if in.Credentials != nil {
		patch.Credentials = &camera.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	u, err := c.Update(patch, a.d.Now())
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInvalid, err.Error())
	}
	if err := a.d.Store.Cameras().Put(ctx, u); err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	a.record(ctx, "camera.update", string(u.ID), map[string]any{"name": in.Name, "group": in.Group, "enabled": in.Enabled, "credentials_changed": in.Credentials != nil})
	a.d.Notify()
	return protocol.Succeed(view(u))
}

func (a *Agent) cameraRemove(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireStore(); r != nil {
		return *r
	}
	var in struct {
		ID string `json:"id"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	if _, err := a.d.Store.Cameras().Get(ctx, camera.ID(in.ID)); err != nil {
		if r := storeErr(err); r != nil {
			return *r
		}
	}
	if err := a.d.Store.Cameras().Delete(ctx, camera.ID(in.ID)); err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	a.record(ctx, "camera.remove", in.ID, nil)
	a.d.Notify()
	return protocol.Succeed(nil)
}

func storeErr(err error) *protocol.Result {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		r := protocol.Fail(protocol.ErrCodeNotFound, "camera not found")
		return &r
	default:
		r := protocol.Fail(protocol.ErrCodeInternal, err.Error())
		return &r
	}
}

// Cloud-pushed state: stored as given and applied where the hive has a
// consumer today (keys → verifier, revocations → verifier). Grants and
// license are kept for #38 (hive-side enforcement) and shown in diagnostics.
const (
	StateKeys    = "keys"
	StateGrants  = "grants"
	StateLicense = "license"
)

func (a *Agent) keysSet(ctx context.Context, args json.RawMessage) protocol.Result {
	var in struct {
		Keys auth.Keys `json:"keys"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	if len(in.Keys) == 0 {
		return protocol.Fail(protocol.ErrCodeInvalid, "at least one key is required")
	}
	if r := a.putState(ctx, StateKeys, args); r != nil {
		return *r
	}
	if a.d.Verifier != nil {
		a.d.Verifier.SetKeys(in.Keys)
	}
	a.record(ctx, "keys.set", "", map[string]any{"count": len(in.Keys)})
	return protocol.Succeed(nil)
}

func (a *Agent) grantsSet(ctx context.Context, args json.RawMessage) protocol.Result {
	return a.storeDocument(ctx, StateGrants, args, "grants.set")
}

func (a *Agent) storeDocument(ctx context.Context, key string, args json.RawMessage, action string) protocol.Result {
	if len(args) == 0 || !json.Valid(args) {
		return protocol.Fail(protocol.ErrCodeBadArgs, "JSON document required")
	}
	if r := a.putState(ctx, key, args); r != nil {
		return *r
	}
	a.record(ctx, action, "", map[string]any{"bytes": len(args)})
	return protocol.Succeed(nil)
}

func (a *Agent) putState(ctx context.Context, key string, doc json.RawMessage) *protocol.Result {
	if r := a.requireStore(); r != nil {
		return r
	}
	if err := a.d.Store.CloudState().Put(ctx, key, doc); err != nil {
		r := protocol.Fail(protocol.ErrCodeInternal, err.Error())
		return &r
	}
	return nil
}

// LoadKeys returns the keys last pushed by the cloud, if any (start-up).
func LoadKeys(ctx context.Context, st *store.Store) (auth.Keys, bool) {
	raw, _, err := st.CloudState().Get(ctx, StateKeys)
	if err != nil {
		return nil, false
	}
	var in struct {
		Keys auth.Keys `json:"keys"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Keys) == 0 {
		return nil, false
	}
	return in.Keys, true
}

func (a *Agent) sessionRevoke(ctx context.Context, args json.RawMessage) protocol.Result {
	var in struct {
		SessionID string    `json:"session_id"`
		Until     time.Time `json:"until"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	if in.SessionID == "" {
		return protocol.Fail(protocol.ErrCodeInvalid, "session_id is required")
	}
	if in.Until.IsZero() {
		in.Until = a.d.Now().Add(auth.MaxOnlineTTL)
	}
	if a.d.Verifier == nil {
		return protocol.Fail(protocol.ErrCodeNotReady, "verifier not available")
	}
	a.d.Verifier.Revoke(in.SessionID, in.Until)
	a.record(ctx, "session.revoke", in.SessionID, map[string]any{"until": in.Until})
	return protocol.Succeed(nil)
}

func (a *Agent) diagBundle(ctx context.Context, _ json.RawMessage) protocol.Result {
	if a.d.Diag == nil {
		return protocol.Fail(protocol.ErrCodeNotReady, "diagnostics not available")
	}
	path, entries, err := a.d.Diag(ctx)
	if err != nil && path == "" {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	var size int64
	if fi, statErr := os.Stat(path); statErr == nil {
		size = fi.Size()
	}
	a.record(ctx, "diag.bundle", path, map[string]any{"entries": len(entries), "bytes": size})
	out := map[string]any{"path": path, "entries": entries, "bytes": size}
	if err != nil {
		out["warning"] = err.Error()
	}
	return protocol.Succeed(out) // the bundle stays on the hive; no video, no upload
}
