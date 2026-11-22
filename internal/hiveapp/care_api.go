package hiveapp

import (
	"errors"
	"net/http"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/store"
)

// CareSource is the part of health.Care the API reads (#163).
type CareSource interface {
	Camera(id string) health.CameraCare
}

// cameraCare is GET /v0/cameras/{id}/care: what camera care says about a
// camera, for everyone who may view it.
func (a *api) cameraCare(w http.ResponseWriter, r *http.Request) {
	if a.d.Care == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "camera care is not running")
		return
	}
	id, ok := cameraScope(w, r, auth.ActionLive)
	if !ok {
		return
	}
	if a.d.Store != nil { // a "*" token covers every id, not cameras that do not exist
		if _, err := a.d.Store.Cameras().Get(r.Context(), camera.ID(id)); errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "camera not found")
			return
		} else if err != nil {
			a.internal(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, a.d.Care.Camera(id))
}

// withCare adds the care summary the app's badges read.
func (a *api) withCare(h health.CameraHealth) health.CameraHealth {
	if a.d.Care != nil {
		h.Care = a.d.Care.Camera(h.Camera).Summary()
	}
	return h
}
