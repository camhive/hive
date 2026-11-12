package hiveapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/export"
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
	// WHEP is the client for the engine's WebRTC endpoint (local live
	// signalling); nil uses a default with a short timeout.
	WHEP *http.Client
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
	api := &api{d: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
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
	mux.Handle("POST /v0/cameras/{id}/ptz/presets/{preset}/goto", api.auth(api.ptzGoto))
	return cors(mux)
}

type api struct {
	d Deps
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
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeError(w, http.StatusUnauthorized, "token_missing", "Authorization: Bearer <token> is required")
			return
		}
		if a.d.Verifier == nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "hive has no cloud keys yet")
			return
		}
		claims, err := a.d.Verifier.Verify(tok)
		if err != nil {
			code, msg := "token_invalid", "access token rejected"
			switch {
			case errors.Is(err, auth.ErrExpired):
				code = "token_expired"
			case errors.Is(err, auth.ErrWrongHive):
				code = "wrong_hive"
			case errors.Is(err, auth.ErrLicense):
				code = "license_required"
			case errors.Is(err, auth.ErrNotYetValid):
				code, msg = "clock_skew", "the hive's clock is behind the cloud; check NTP on the hive"
			}
			a.d.Logger.Warn("access token rejected", "code", code, "path", r.URL.Path, "err", err)
			writeError(w, http.StatusUnauthorized, code, msg)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
	})
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

func toView(c camera.Camera, claims auth.Claims) cameraView {
	v := cameraView{
		ID: string(c.ID), Name: c.Name, Group: c.Group,
		Source:  sourceView{Kind: string(c.Source.Kind), Address: c.Source.Address},
		Streams: map[string]camera.StreamSpec{}, Enabled: c.Enabled, RecordAudio: c.RecordAudio, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
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
			out = append(out, toView(c, claims))
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
	writeJSON(w, http.StatusOK, toView(c, claims))
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
	writeJSON(w, http.StatusCreated, toView(c, claims))
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
	patch := camera.Patch{Name: in.Name, Group: in.Group, Enabled: in.Enabled, RecordAudio: in.RecordAudio}
	if in.Credentials != nil {
		patch.Credentials = &camera.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	u, err := c.Update(patch, time.Now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	}
	if err := a.d.Store.Cameras().Put(r.Context(), u); err != nil {
		a.internal(w, err)
		return
	}
	a.record(r, "camera.update", string(u.ID), map[string]any{"name": in.Name, "group": in.Group, "enabled": in.Enabled, "record_audio": in.RecordAudio, "credentials_changed": in.Credentials != nil})
	a.d.Notify()
	writeJSON(w, http.StatusOK, toView(u, claims))
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

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": a.d.Version})
}

// record appends to the local audit trail (ADR-0011). Details must never
// contain secrets; callers pass only what the owner may see.
func (a *api) record(r *http.Request, action, target string, details map[string]any) {
	raw, _ := json.Marshal(details)
	if err := a.d.Store.Actions().Append(r.Context(), store.Action{Actor: claimsFrom(r).Account, Action: action, Target: target, Details: raw}); err != nil {
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
