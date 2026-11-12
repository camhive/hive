package hiveapp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/video"
)

// Local WebRTC signalling (ADR-0013, #56): at home the web app starts live
// video here instead of through the cloud, with an online or offline token;
// requests through the relay still get the plan's quality. The browser posts
// its WHEP offer here, the hive forwards it to the video engine and
// returns the answer; candidates and close follow the same path the cloud
// uses over the tunnel (hiveagent/signal.go), so the engine sees no
// difference.

const whepTimeout = 15 * time.Second

func (a *api) whepClient() *http.Client {
	if a.d.WHEP != nil {
		return a.d.WHEP
	}
	return &http.Client{Timeout: whepTimeout}
}

func (a *api) requireEngine(w http.ResponseWriter) bool {
	if a.d.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "video engine not running")
		return false
	}
	return true
}

type liveOffer struct {
	Stream string `json:"stream"`
	SDP    string `json:"sdp"`
}

// liveOffer answers POST /v0/cameras/{id}/live/offer with the engine's SDP answer and a session handle.
func (a *api) liveOffer(w http.ResponseWriter, r *http.Request) {
	if !a.requireEngine(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionLive)
	if !ok {
		return
	}
	var in liveOffer
	if !decodeJSON(w, r, &in) {
		return
	}
	if !strings.Contains(in.SDP, "v=0") {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "sdp must be a WHEP offer")
		return
	}
	cam, ok := a.liveCamera(r, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "camera is not available")
		return
	}
	stream := servedStream(r, cam, in.Stream)
	answer, session, err := hiveagent.ForwardWHEP(r.Context(), a.whepClient(), a.d.Engine.WHEPURL(video.CameraID(id), video.Stream(stream)), in.SDP)
	if errors.Is(err, hiveagent.ErrStreamUnavailable) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "engine", err.Error())
		return
	}
	a.record(r, "live.local", id, map[string]any{"stream": stream})
	writeJSON(w, http.StatusCreated, map[string]string{"sdp": answer, "session": session})
}

// sessionURL validates a handle: it must point at our engine and at a WHEP resource.
func (a *api) sessionURL(w http.ResponseWriter, handle string) (string, bool) {
	u, err := url.Parse(handle)
	base, _ := url.Parse(a.d.Engine.WHEPURL("x", video.Main))
	if err != nil || handle == "" || base == nil || u.Host != base.Host || !strings.Contains(u.Path, "/whep/") {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "unknown signalling session")
		return "", false
	}
	return handle, true
}

type liveSessionInput struct {
	Session    string `json:"session"`
	Candidates string `json:"candidates"`
}

// liveCandidates serves POST /v0/live/candidates: trickled ICE to the engine.
func (a *api) liveCandidates(w http.ResponseWriter, r *http.Request) {
	in, ok := a.liveSessionInput(w, r)
	if !ok {
		return
	}
	if in.Candidates == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "candidates are required")
		return
	}
	a.forwardToEngine(w, r, http.MethodPatch, in.Session, "application/trickle-ice-sdpfrag", strings.NewReader(in.Candidates))
}

// liveClose serves POST /v0/live/close: ends the engine session.
func (a *api) liveClose(w http.ResponseWriter, r *http.Request) {
	in, ok := a.liveSessionInput(w, r)
	if !ok {
		return
	}
	a.forwardToEngine(w, r, http.MethodDelete, in.Session, "", nil)
}

func (a *api) liveSessionInput(w http.ResponseWriter, r *http.Request) (liveSessionInput, bool) {
	var in liveSessionInput
	if !a.requireEngine(w) || !decodeJSON(w, r, &in) {
		return in, false
	}
	return in, true
}

// forwardToEngine sends one request to a validated session handle and maps the engine's answer.
func (a *api) forwardToEngine(w http.ResponseWriter, r *http.Request, method, handle, contentType string, body io.Reader) {
	target, ok := a.sessionURL(w, handle)
	if !ok {
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.whepClient().Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "engine", "whep: "+err.Error())
		return
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		writeError(w, http.StatusNotFound, "not_found", "signalling session is gone")
	case resp.StatusCode >= 300:
		writeError(w, http.StatusBadGateway, "engine", fmt.Sprintf("whep answered %d", resp.StatusCode))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
