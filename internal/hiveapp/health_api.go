package hiveapp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/health"
)

// HealthSource is the part of health.Monitor the API reads.
type HealthSource interface {
	Camera(id string) health.CameraHealth
	All() []health.CameraHealth
}

func (a *api) requireHealth(w http.ResponseWriter) bool {
	if a.d.Health == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "health monitor is not running")
		return false
	}
	return true
}

func (a *api) cameraHealth(w http.ResponseWriter, r *http.Request) {
	if !a.requireHealth(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionLive)
	if !ok {
		return
	}
	h := a.d.Health.Camera(id)
	if h.Camera == "" {
		h = health.CameraHealth{Camera: id, Streams: map[string]health.StreamHealth{}}
	}
	writeJSON(w, http.StatusOK, h)
}

// healthOverview lists the health of every camera the token may view.
func (a *api) healthOverview(w http.ResponseWriter, r *http.Request) {
	if !a.requireHealth(w) {
		return
	}
	claims := claimsFrom(r)
	all := a.d.Health.All()
	out := make([]health.CameraHealth, 0, len(all))
	for _, h := range all {
		if claims.Allows(h.Camera, auth.ActionLive) {
			out = append(out, h)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type eventView struct {
	ID     int64           `json:"id"`
	Kind   string          `json:"kind"`
	At     time.Time       `json:"at"`
	Data   json.RawMessage `json:"data"`
	Camera string          `json:"camera"`
}

func (a *api) cameraEvents(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionArchive)
	if !ok {
		return
	}
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		writeError(w, http.StatusBadRequest, "invalid", "from and to must be RFC 3339 with to > from")
		return
	}
	events, err := a.d.Store.Events().Range(r.Context(), id, from, to)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]eventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventView{ID: e.ID, Kind: e.Kind, At: e.At, Data: e.Data, Camera: e.Camera})
	}
	writeJSON(w, http.StatusOK, out)
}
