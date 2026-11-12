package hiveapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/ptz"
)

// PTZController is what the API needs from ptz.Controller.
type PTZController interface {
	Supported(ctx context.Context, cam camera.Camera) (bool, error)
	Move(ctx context.Context, cam camera.Camera, pan, tilt, zoom float64) error
	Stop(ctx context.Context, cam camera.Camera) error
	Presets(ctx context.Context, cam camera.Camera) ([]ptz.Preset, error)
	GotoPreset(ctx context.Context, cam camera.Camera, preset string) error
}

// ptzCamera loads the camera after scope checks; nil when a response was written.
func (a *api) ptzCamera(w http.ResponseWriter, r *http.Request, action auth.Action) (camera.Camera, bool) {
	if a.d.PTZ == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "ptz controller is not running")
		return camera.Camera{}, false
	}
	if !a.requireStore(w) {
		return camera.Camera{}, false
	}
	id, ok := cameraScope(w, r, action)
	if !ok {
		return camera.Camera{}, false
	}
	c, err := a.d.Store.Cameras().Get(r.Context(), camera.ID(id))
	if a.storeErr(w, err) {
		return camera.Camera{}, false
	}
	return c, true
}

// ptzErr maps controller errors to responses; returns true when handled.
func (a *api) ptzErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ptz.ErrUnsupported):
		writeError(w, http.StatusConflict, "unsupported", "camera does not support PTZ")
	case errors.Is(err, ptz.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	default:
		a.d.Logger.Warn("ptz", "err", err)
		writeError(w, http.StatusBadGateway, "camera", "camera did not accept the command")
	}
	return true
}

func (a *api) ptzInfo(w http.ResponseWriter, r *http.Request) {
	c, ok := a.ptzCamera(w, r, auth.ActionLive)
	if !ok {
		return
	}
	if !a.licensed().Bool(entitlements.PTZEnabled) {
		writeJSON(w, http.StatusOK, map[string]bool{"supported": false, "licensed": false})
		return
	}
	supported, err := a.d.PTZ.Supported(r.Context(), c)
	if a.ptzErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"supported": supported})
}

type moveInput struct {
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
}

func (a *api) ptzMove(w http.ResponseWriter, r *http.Request) {
	c, ok := a.ptzCamera(w, r, auth.ActionPTZ)
	if !ok {
		return
	}
	if !a.licensed().Bool(entitlements.PTZEnabled) {
		writeError(w, http.StatusForbidden, "plan_limit", "PTZ control is not included in the owner's plan")
		return
	}
	var in moveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if a.ptzErr(w, a.d.PTZ.Move(r.Context(), c, in.Pan, in.Tilt, in.Zoom)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) ptzStop(w http.ResponseWriter, r *http.Request) {
	c, ok := a.ptzCamera(w, r, auth.ActionPTZ)
	if !ok {
		return
	}
	if !a.licensed().Bool(entitlements.PTZEnabled) {
		writeError(w, http.StatusForbidden, "plan_limit", "PTZ control is not included in the owner's plan")
		return
	}
	if a.ptzErr(w, a.d.PTZ.Stop(r.Context(), c)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) ptzPresets(w http.ResponseWriter, r *http.Request) {
	c, ok := a.ptzCamera(w, r, auth.ActionPTZ)
	if !ok {
		return
	}
	if !a.licensed().Bool(entitlements.PTZEnabled) {
		writeError(w, http.StatusForbidden, "plan_limit", "PTZ control is not included in the owner's plan")
		return
	}
	presets, err := a.d.PTZ.Presets(r.Context(), c)
	if a.ptzErr(w, err) {
		return
	}
	out := make([]map[string]string, 0, len(presets))
	for _, p := range presets {
		out = append(out, map[string]string{"token": p.Token, "name": p.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) ptzGoto(w http.ResponseWriter, r *http.Request) {
	c, ok := a.ptzCamera(w, r, auth.ActionPTZ)
	if !ok {
		return
	}
	if a.ptzErr(w, a.d.PTZ.GotoPreset(r.Context(), c, r.PathValue("preset"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
