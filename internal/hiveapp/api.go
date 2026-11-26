package hiveapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/recording"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// Deps are what the local API needs. Store may be nil before the hive is
// fully initialised; camera endpoints then answer 503 not_ready.
type Deps struct {
	Store    *store.Store
	Verifier *auth.Verifier
	Version  string
	Notify   func() // called after a camera change (ingest.Notify)
	Logger   *slog.Logger

	// Archive (nil until the engine is up; endpoints answer 503 not_ready).
	Index    archiveIndexer
	Engine   video.Engine
	Playback export.Fetch // spans of footage (ArchiveFetch)
	Exports  *export.Manager
	Location *time.Location // the hive's local zone for calendars; default UTC
	Health   HealthSource   // nil until the monitor runs
	PTZ      PTZController  // nil until the controller exists
	// Licensed returns the entitlements the hive enforces locally
	// (ADR-0006); nil means the Free seed.
	Licensed func() entitlements.Set
	// Discovery finds LAN cameras for the owner; nil answers 503 not_ready.
	Discovery Discoverer
	// Deletions removes footage on the owner's request (ADR-0016); nil
	// answers 503 not_ready.
	Deletions Deleter
	// Locator tells what device answers at a host (ADR-0017); nil skips the
	// identity check when the owner changes a camera's address.
	Locator CameraLocator
	// WHEP is the client for the engine's WebRTC endpoint (local live
	// signalling); nil uses a default with a short timeout.
	WHEP *http.Client
	// Local issues and verifies the owner's local sessions (#57); nil
	// refuses local tokens and local sign-in.
	Local *auth.LocalSessions
	// Updates lets the owner manage hive-updater (#82); nil answers 503
	// not_ready.
	Updates *UpdateControl
	// Vault is the archive key (ADR-0019); nil: encryption is not
	// available and its state is off.
	Vault ArchiveVault
	// Emit sends an event to the cloud (hiveagent.Agent.Emit): the audit
	// of archive key changes. Nil sends nothing.
	Emit func(name string, data any)
	// Autolock locks the unlocked archive after inactivity or when the
	// owner left home (#147); nil: the endpoints answer 503 not_ready.
	Autolock *archivekey.Autolock
	// Storage is the archive disk and the hive-wide retention settings
	// (#150); nil answers 503 not_ready.
	Storage *StorageControl
	// Move is where the hive reads the note of hive move-data (#155);
	// nil: no move was ever started.
	Move *MoveInfo
	// Care is camera care (#163); nil: GET /v0/cameras/{id}/care answers
	// 503 not_ready and the health views carry no care summary.
	Care CareSource
	// Motion says which cameras the hive calmed (#157, motion.Hub); nil:
	// none.
	Motion CalmReader
	// Recording is the recording watchdog's verdict (#172); nil: recording.
	Recording func() recording.State
}

// CalmReader tells since when the hive calmed a camera's motion detection
// because it raised too many episodes (#157).
type CalmReader interface {
	CalmedSince(cam string) (time.Time, bool)
}

// licensed is the effective local entitlement set.
func (a *api) licensed() entitlements.Set {
	if a.d.Licensed != nil {
		return a.d.Licensed()
	}
	return entitlements.OnPlan(entitlements.PlanFree).Set
}

// NewAPI builds the hive's local HTTP API (api/openapi.yaml). /healthz is
// public; everything under /v0 requires an access token (ADR-0004).
func NewAPI(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Notify == nil {
		d.Notify = func() {}
	}
	if d.Location == nil {
		d.Location = time.UTC
	}
	api := &api{d: d, logins: newLoginLimiter(time.Now)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", landing)
	mux.HandleFunc("GET /healthz", api.health)
	mux.Handle("GET /v0/hive", api.auth(api.getHive))
	mux.Handle("GET /v0/hive/updates", api.auth(api.getUpdates))
	mux.Handle("PUT /v0/hive/updates", api.auth(api.setUpdates))
	mux.Handle("POST /v0/hive/updates/install", api.auth(api.installUpdate))
	mux.Handle("GET /v0/cameras", api.auth(api.listCameras))
	mux.Handle("POST /v0/cameras", api.auth(api.createCamera))
	mux.Handle("GET /v0/cameras/{id}", api.auth(api.getCamera))
	mux.Handle("PATCH /v0/cameras/{id}", api.auth(api.patchCamera))
	mux.Handle("DELETE /v0/cameras/{id}", api.auth(api.deleteCamera))
	mux.Handle("GET /v0/cameras/{id}/archive/calendar", api.auth(api.calendar))
	mux.Handle("GET /v0/cameras/{id}/archive/timeline", api.auth(api.timeline))
	mux.Handle("GET /v0/cameras/{id}/archive/play", api.auth(api.play))
	mux.Handle("GET /v0/cameras/{id}/archive/audio", api.auth(api.audio))
	mux.Handle("POST /v0/cameras/{id}/exports", api.auth(api.createExport))
	mux.Handle("GET /v0/exports", api.auth(api.listExports))
	mux.Handle("POST /v0/archive/deletions", api.auth(api.deleteRecordings))
	mux.Handle("GET /v0/archive/encryption", api.auth(api.encryptionStatus))
	mux.Handle("POST /v0/archive/encryption", api.auth(api.enableEncryption))
	mux.Handle("POST /v0/archive/encryption/unlock", api.auth(api.unlockArchive))
	mux.Handle("POST /v0/archive/encryption/lock", api.auth(api.lockArchive))
	mux.Handle("POST /v0/archive/encryption/passphrase", api.auth(api.changePassphrase))
	mux.Handle("GET /v0/archive/encryption/key-file", api.auth(api.keyFile))
	mux.Handle("GET /v0/exports/{id}", api.auth(api.getExport))
	mux.Handle("GET /v0/exports/{id}/file", api.auth(api.exportFile))
	mux.Handle("GET /v0/cameras/{id}/health", api.auth(api.cameraHealth))
	mux.Handle("GET /v0/cameras/{id}/events", api.auth(api.cameraEvents))
	mux.Handle("GET /v0/health/cameras", api.auth(api.healthOverview))
	mux.Handle("GET /v0/actions", api.auth(api.listActions))
	mux.Handle("POST /v0/cameras/{id}/live/offer", api.auth(api.liveOffer))
	mux.Handle("POST /v0/live/candidates", api.auth(api.liveCandidates))
	mux.Handle("POST /v0/live/close", api.auth(api.liveClose))
	mux.Handle("GET /v0/discovery", api.auth(api.discover))
	mux.Handle("POST /v0/discovery/probe", api.auth(api.probe))
	mux.Handle("GET /v0/cameras/{id}/ptz", api.auth(api.ptzInfo))
	mux.Handle("POST /v0/cameras/{id}/ptz/move", api.auth(api.ptzMove))
	mux.Handle("POST /v0/cameras/{id}/ptz/stop", api.auth(api.ptzStop))
	mux.Handle("GET /v0/cameras/{id}/ptz/presets", api.auth(api.ptzPresets))
	mux.Handle("GET /v0/local-login", api.auth(api.localLoginStatus))
	mux.Handle("PUT /v0/local-login", api.auth(api.setLocalLogin))
	mux.Handle("DELETE /v0/local-login", api.auth(api.disableLocalLogin))
	mux.HandleFunc("POST /v0/local-login/sessions", api.localSignIn)
	mux.Handle("POST /v0/cameras/{id}/ptz/presets/{preset}/goto", api.auth(api.ptzGoto))
	mux.Handle("PATCH /v0/archive/encryption/autolock", api.auth(api.setAutolock))
	mux.Handle("POST /v0/archive/encryption/presence", api.auth(api.presence))
	mux.Handle("GET /v0/storage", api.auth(api.getStorage))
	mux.Handle("PATCH /v0/hive/storage", api.auth(api.patchStorage))
	mux.Handle("GET /v0/hive/move", api.auth(api.getMove))
	mux.Handle("GET /v0/cameras/{id}/care", api.auth(api.cameraCare))
	return cors(whileMoving(d.Move, mux))
}

type api struct {
	d      Deps
	logins *loginLimiter
	hashMu sync.Mutex // one argon2id verification at a time bounds memory under a flood
	// autolockMu: one change of the automatic lock at a time (#147).
	autolockMu sync.Mutex
}

// --- errors -------------------------------------------------------------

// apiError is the JSON body of every error response: a stable machine code
// and a human message. Codes are part of the API contract.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Code: code, Message: msg})
}

// --- auth ----------------------------------------------------------------

type ctxKey struct{}

func claimsFrom(r *http.Request) auth.Claims {
	c, _ := r.Context().Value(ctxKey{}).(auth.Claims)
	return c
}

// auth verifies the bearer token and puts the claims into the context.
func (a *api) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			writeError(w, http.StatusUnauthorized, "token_missing", "Authorization: Bearer <token> is required")
			return
		}
		claims, err := a.verify(r, tok)
		if err != nil {
			status, code, msg := tokenError(err)
			a.d.Logger.Warn("access token rejected", "code", code, "path", r.URL.Path, "err", err)
			writeError(w, status, code, msg)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
	})
}

var (
	errLANOnly  = errors.New("local sign-in and local sessions work only on the hive's LAN listener")
	errNoKeys   = errors.New("hive has no cloud keys yet")
	errNoLocals = fmt.Errorf("%w: this hive issues no local sessions", auth.ErrInvalidToken)
)

// verify checks a cloud token with the cloud's keys, or a local token (#57)
// with the hive's own key and against the stored credential, the latter
// only on the LAN listener.
func (a *api) verify(r *http.Request, tok string) (auth.Claims, error) {
	if auth.IsLocalToken(tok) {
		switch {
		case !onLAN(r):
			return auth.Claims{}, errLANOnly
		case a.d.Local == nil || a.d.Store == nil:
			return auth.Claims{}, errNoLocals
		}
		c, err := a.d.Local.Verify(tok)
		if err == nil {
			err = a.stillCurrent(r.Context(), c)
		}
		return c, err
	}
	if a.d.Verifier == nil {
		return auth.Claims{}, errNoKeys
	}
	return a.d.Verifier.Verify(tok)
}

// tokenError maps a verification error to the response.
func tokenError(err error) (status int, code, msg string) {
	switch {
	case errors.Is(err, errLANOnly):
		return http.StatusForbidden, "lan_only", errLANOnly.Error()
	case errors.Is(err, errNoKeys):
		return http.StatusServiceUnavailable, "not_ready", errNoKeys.Error()
	case errors.Is(err, auth.ErrExpired):
		return http.StatusUnauthorized, "token_expired", "access token rejected"
	case errors.Is(err, auth.ErrWrongHive):
		return http.StatusUnauthorized, "wrong_hive", "access token rejected"
	case errors.Is(err, auth.ErrLicense):
		return http.StatusUnauthorized, "license_required", "access token rejected"
	case errors.Is(err, auth.ErrNotYetValid):
		return http.StatusUnauthorized, "clock_skew", "the hive's clock is behind the cloud; check NTP on the hive"
	}
	return http.StatusUnauthorized, "token_invalid", "access token rejected"
}

func (a *api) requireStore(w http.ResponseWriter) bool {
	if a.d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "hive storage is not initialised")
		return false
	}
	return true
}

// --- cameras -------------------------------------------------------------

// cameraView is the API shape of a camera. Credentials show the username
// only, and only to owners; the password never leaves the hive.
type cameraView struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Group       string                       `json:"group"`
	Source      sourceView                   `json:"source"`
	Credentials *credentialsView             `json:"credentials,omitempty"`
	Streams     map[string]camera.StreamSpec `json:"streams"`
	Enabled     bool                         `json:"enabled"`
	RecordAudio bool                         `json:"record_audio"`
	Retention   *int                         `json:"retention_days,omitempty"` // absent: the hive-wide value (#61); every role: it says how far back the archive goes, which the calendar shows them anyway
	Identity    *camera.Identity             `json:"identity,omitempty"`       // owner only (ADR-0017)
	Network     string                       `json:"network,omitempty"`
	Sensitivity string                       `json:"sensitivity"`                   // #157
	CalmedSince *time.Time                   `json:"motion_calmed_since,omitempty"` // #157: the hive raised the threshold one step since then
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

type sourceView struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

type credentialsView struct {
	Username string `json:"username"`
}

func (a *api) toView(c camera.Camera, claims auth.Claims) cameraView {
	v := cameraView{
		ID: string(c.ID), Name: c.Name, Group: c.Group,
		Source:  sourceView{Kind: string(c.Source.Kind), Address: c.Source.Address},
		Streams: map[string]camera.StreamSpec{}, Enabled: c.Enabled, RecordAudio: c.RecordAudio, Retention: c.Retention.Days, Network: string(c.Network),
		Sensitivity: c.Sensitivity.String(), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if a.d.Motion != nil {
		if since, ok := a.d.Motion.CalmedSince(string(c.ID)); ok {
			v.CalmedSince = &since
		}
	}
	if claims.Role == auth.RoleOwner {
		id := c.Identity
		v.Identity = &id
	}
	for k, s := range c.Streams {
		v.Streams[string(k)] = s
	}
	if claims.Role == auth.RoleOwner && c.Credentials.Username != "" {
		v.Credentials = &credentialsView{Username: c.Credentials.Username}
	}
	return v
}

type cameraInput struct {
	Name        string     `json:"name"`
	Group       string     `json:"group"`
	Source      sourceView `json:"source"`
	Credentials *struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"credentials"`
	// Streams names the RTSP URLs per stream (main, sub) for ONVIF cameras,
	// as picked from a probe; RTSP sources derive main from the address.
	Streams map[string]streamInput `json:"streams"`
}

type streamInput struct {
	URL          string `json:"url"`
	ProfileToken string `json:"profile_token"`
	Codec        string `json:"codec"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type cameraPatch struct {
	Name        *string `json:"name"`
	Group       *string `json:"group"`
	Enabled     *bool   `json:"enabled"`
	RecordAudio *bool   `json:"record_audio"`
	Credentials *struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"credentials"`
	// Host moves the camera to another host (ADR-0017); ReplaceIdentity
	// accepts a device that is not the known camera (a replacement).
	Host            *string        `json:"host"`
	ReplaceIdentity bool           `json:"replace_identity"`
	Retention       retentionInput `json:"retention_days"`
	// SubStream sets the sub stream's RTSP URL; "" removes it (#63).
	SubStream *string `json:"sub_stream"`
	// Sensitivity is low, normal or high (#157).
	Sensitivity *string `json:"sensitivity"`
}

// retentionInput is retention_days in a camera patch (#61): absent leaves
// it, null puts the camera back on the hive-wide value, a number of days
// gives the camera its own.
type retentionInput struct {
	set  bool
	days *int
}

// UnmarshalJSON runs only when the key is present, null included.
func (r *retentionInput) UnmarshalJSON(b []byte) error {
	r.set = true
	return json.Unmarshal(b, &r.days)
}

func (r retentionInput) patch() *camera.Retention {
	if !r.set {
		return nil
	}
	return &camera.Retention{Days: r.days}
}

// detail is the action log's value: absent, "hive" or the days.
func (r retentionInput) detail() any {
	switch {
	case !r.set:
		return nil
	case r.days == nil:
		return "hive"
	}
	return *r.days
}

// applyPatch returns the camera with the patch applied and, when the host
// changes, moved there; it writes the error response when it refuses.
func (a *api) applyPatch(w http.ResponseWriter, r *http.Request, c camera.Camera, in cameraPatch) (camera.Camera, bool) {
	var u camera.Camera
	p, err := in.patch()
	if err == nil {
		u, err = c.Update(p, time.Now())
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return camera.Camera{}, false
	}
	if in.Host == nil || *in.Host == c.Host() {
		return u, true
	}
	return a.moveCamera(w, r, u, *in.Host, in.ReplaceIdentity)
}

func (in cameraPatch) patch() (camera.Patch, error) {
	p := camera.Patch{Name: in.Name, Group: in.Group, Enabled: in.Enabled, RecordAudio: in.RecordAudio, Retention: in.Retention.patch(), SubStream: in.SubStream}
	if in.Credentials != nil {
		p.Credentials = &camera.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	if in.Sensitivity != nil {
		s, err := camera.ParseSensitivity(*in.Sensitivity)
		if err != nil {
			return camera.Patch{}, err
		}
		p.Sensitivity = &s
	}
	return p, nil
}

// changesFields reports whether the patch changes more than the host.
func (in cameraPatch) changesFields() bool {
	return in.Name != nil || in.Group != nil || in.Enabled != nil || in.RecordAudio != nil || in.Credentials != nil || in.Retention.set || in.SubStream != nil || in.Sensitivity != nil
}

// recordPatch logs a move and an update separately, each only when it happened.
func (a *api) recordPatch(r *http.Request, old, now camera.Camera, in cameraPatch) {
	if now.Host() != old.Host() {
		a.record(r, "camera.move", string(now.ID), map[string]any{"old": old.Host(), "new": now.Host(), "replaced_identity": in.ReplaceIdentity})
	}
	if in.changesFields() {
		a.record(r, "camera.update", string(now.ID), map[string]any{"name": in.Name, "group": in.Group, "enabled": in.Enabled, "record_audio": in.RecordAudio,
			"credentials_changed": in.Credentials != nil, "retention_days": in.Retention.detail(), "sub_stream": in.SubStream, "sensitivity": in.Sensitivity})
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return false
	}
	return true
}

func (a *api) listCameras(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	claims := claimsFrom(r)
	cams, err := a.d.Store.Cameras().List(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]cameraView, 0, len(cams))
	for _, c := range cams {
		if claims.Allows(string(c.ID), auth.ActionLive) {
			out = append(out, a.toView(c, claims))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) getCamera(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	claims := claimsFrom(r)
	id := r.PathValue("id")
	if !claims.Allows(id, auth.ActionLive) {
		writeError(w, http.StatusNotFound, "not_found", "camera not found") // ungranted looks absent
		return
	}
	c, err := a.d.Store.Cameras().Get(r.Context(), camera.ID(id))
	if a.storeErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, a.toView(c, claims))
}

func (a *api) createCamera(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	claims := claimsFrom(r)
	if !claims.Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can add cameras")
		return
	}
	var in cameraInput
	if !decodeJSON(w, r, &in) {
		return
	}
	spec := camera.Spec{Name: in.Name, Group: in.Group, Source: camera.Source{Kind: camera.SourceKind(in.Source.Kind), Address: in.Source.Address}}
	if in.Credentials != nil {
		spec.Credentials = camera.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	if len(in.Streams) > 0 {
		spec.Streams = map[camera.Stream]camera.StreamSpec{}
		for name, st := range in.Streams {
			spec.Streams[camera.Stream(name)] = camera.StreamSpec{URL: st.URL, ProfileToken: st.ProfileToken, Codec: st.Codec, Width: st.Width, Height: st.Height}
		}
	}
	c, err := camera.New(spec, time.Now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	}
	if err := a.d.Store.Cameras().Put(r.Context(), c); err != nil {
		a.internal(w, err)
		return
	}
	a.record(r, "camera.add", string(c.ID), map[string]any{"name": c.Name, "kind": c.Source.Kind, "address": c.Source.Address})
	a.d.Notify()
	writeJSON(w, http.StatusCreated, a.toView(c, claims))
}

func (a *api) patchCamera(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	claims := claimsFrom(r)
	if !claims.Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can change cameras")
		return
	}
	var in cameraPatch
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := a.d.Store.Cameras().Get(r.Context(), camera.ID(r.PathValue("id")))
	if a.storeErr(w, err) {
		return
	}
	u, ok := a.applyPatch(w, r, c, in)
	if !ok {
		return
	}
	if err := a.d.Store.Cameras().Put(r.Context(), u); err != nil {
		a.internal(w, err)
		return
	}
	a.recordPatch(r, c, u, in)
	a.d.Notify()
	writeJSON(w, http.StatusOK, a.toView(u, claims))
}

func (a *api) deleteCamera(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	if !claimsFrom(r).Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can remove cameras")
		return
	}
	if err := a.d.Store.Cameras().Delete(r.Context(), camera.ID(r.PathValue("id"))); err != nil {
		a.internal(w, err)
		return
	}
	a.record(r, "camera.remove", r.PathValue("id"), nil)
	a.d.Notify()
	w.WriteHeader(http.StatusNoContent)
}

// storeErr maps store errors to responses; returns true when handled.
func (a *api) storeErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "camera not found")
	default:
		a.internal(w, err)
	}
	return true
}

func (a *api) internal(w http.ResponseWriter, err error) {
	a.d.Logger.Error("api", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

// Health is GET /healthz. Recording and Updates go only to programs on
// this machine (localTool), never to web pages there: hive-updater checks
// an update with them (ADR-0018), and `hive status` reports the running
// hive, which may be newer than the package's binary (#109).
type Health struct {
	Status       string         `json:"status"`
	Version      string         `json:"version"`
	Recording    *int           `json:"recording,omitempty"`
	NotRecording *NotRecording  `json:"not_recording,omitempty"`
	Updates      *UpdateReports `json:"updates,omitempty"`
}

// NotRecording says since when and why the hive does not record (#172).
// Detail, what was seen (paths, errors), goes only to programs on this
// machine.
type NotRecording struct {
	Since  time.Time        `json:"since"`
	Reason recording.Reason `json:"reason"`
	Detail string           `json:"detail,omitempty"`
}

// notRecording is the watchdog's verdict when the hive does not record.
func (a *api) notRecording(detail bool) *NotRecording {
	if a.d.Recording == nil {
		return nil
	}
	s := a.d.Recording()
	if s.Recording() {
		return nil
	}
	nr := &NotRecording{Since: s.Since.UTC(), Reason: s.Reason}
	if detail {
		nr.Detail = s.Detail
	}
	return nr
}

// health answers liveness and version; to a local tool also how many
// cameras record, whether the hive records at all and what the updater
// reported. Not recording leaves the status ok: hive-updater must not
// roll an update back because a disk vanished.
func (a *api) health(w http.ResponseWriter, r *http.Request) {
	out := Health{Status: "ok", Version: a.d.Version}
	if localTool(r) {
		out.Recording, out.NotRecording, out.Updates = a.recording(), a.notRecording(true), a.healthUpdates(r)
	}
	writeJSON(w, http.StatusOK, out)
}

// recording counts the cameras recording now; nil without a video engine.
func (a *api) recording() *int {
	if a.d.Health == nil {
		return nil
	}
	n := 0
	for _, c := range a.d.Health.All() {
		if c.Enabled && c.Online {
			n++
		}
	}
	return &n
}

// healthUpdates is what the updater reported; nil when it cannot be read
// within a second: liveness must not depend on the action log, or a slow
// database could make hive-updater (5 s timeout) roll an update back.
func (a *api) healthUpdates(r *http.Request) *UpdateReports {
	if a.d.Store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	u, err := a.updateReports(ctx)
	if err != nil {
		a.d.Logger.Warn("healthz: action log", "err", err)
		return nil
	}
	return &u
}

// localTool: a request from a program on this machine (hive-updater, the
// hive CLI), not from a web page there: a browser on the hive's machine is
// loopback too, and sends Origin on every cross-origin request.
func localTool(r *http.Request) bool {
	return fromLoopback(r) && r.Header.Get("Origin") == ""
}

// fromLoopback: a request from this machine that did not come through the relay.
func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback() && !Relayed(r.Context())
}

// record appends to the local audit trail (ADR-0011). Details must never
// contain secrets; callers pass only what the owner may see.
func (a *api) record(r *http.Request, action, target string, details map[string]any) {
	a.recordAs(r, claimsFrom(r).Account, action, target, details)
}

// recordAs is record for a request without a token (local sign-in).
func (a *api) recordAs(r *http.Request, actor, action, target string, details map[string]any) {
	raw, _ := json.Marshal(details)
	if err := a.d.Store.Actions().Append(r.Context(), store.Action{Actor: actor, Action: action, Target: target, Details: raw}); err != nil {
		a.d.Logger.Error("action log", "err", err)
	}
}

type actionView struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Details json.RawMessage `json:"details"`
}

// listActions returns the hive's activity, owner only.
func (a *api) listActions(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	if !claimsFrom(r).Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can read the activity log")
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	actions, err := a.d.Store.Actions().Recent(r.Context(), limit)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]actionView, 0, len(actions))
	for _, x := range actions {
		out = append(out, actionView{ID: x.ID, At: x.At, Actor: x.Actor, Action: x.Action, Target: x.Target, Details: x.Details})
	}
	writeJSON(w, http.StatusOK, out)
}
