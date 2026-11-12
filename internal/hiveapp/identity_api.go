package hiveapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// CameraLocator tells what device answers at a host (ADR-0017); the
// locate.Locator implements it.
type CameraLocator interface {
	Probe(ctx context.Context, c camera.Camera, host string) (camera.Identity, camera.Network)
}

// moveCamera puts the camera at host after checking that the device there
// is the same camera, unless the owner says it replaced it. It writes the
// error response and returns false when it refuses.
func (a *api) moveCamera(w http.ResponseWriter, r *http.Request, c camera.Camera, host string, replace bool) (camera.Camera, bool) {
	moved, err := c.MoveTo(host, time.Now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return camera.Camera{}, false
	}
	if a.d.Locator != nil {
		seen, network := a.d.Locator.Probe(r.Context(), c, moved.Host())
		learned, conflicts := moved.Learn(seen, time.Now())
		switch {
		case len(conflicts) > 0 && !replace:
			writeError(w, http.StatusConflict, "camera_mismatch",
				fmt.Sprintf("another device answers at %s (%s differ); confirm that the camera was replaced", host, strings.Join(conflicts, ", ")))
			return camera.Camera{}, false
		case replace:
			fresh := moved
			fresh.Identity = camera.Identity{}
			learned, _ = fresh.Learn(seen, time.Now())
		}
		moved = learned
		if network != "" {
			moved.Network = network
		}
	}
	return moved, true
}
