package hiveapp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/retention"
)

// Deleter removes footage the owner asks to delete (ADR-0016); the
// retention Sweeper implements it.
type Deleter interface {
	Delete(ctx context.Context, sc retention.Scope, dryRun bool) (retention.Plan, error)
}

type deletionInput struct {
	Cameras        *[]string  `json:"cameras"` // absent: every camera
	From           *time.Time `json:"from"`    // absent: from the beginning
	To             *time.Time `json:"to"`      // absent or later: now
	IncludeExports bool       `json:"include_exports"`
	DryRun         bool       `json:"dry_run"`
}

type cameraDeletionView struct {
	Camera   string    `json:"camera"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Segments int       `json:"segments"`
	Bytes    int64     `json:"bytes"`
	Pending  int       `json:"pending"`
}

type deletionView struct {
	Cameras []cameraDeletionView `json:"cameras"`
	Exports struct {
		Count int   `json:"count"`
		Bytes int64 `json:"bytes"`
	} `json:"exports"`
}

// deleteRecordings previews (dry_run) or deletes the owner's footage.
func (a *api) deleteRecordings(w http.ResponseWriter, r *http.Request) {
	if !claimsFrom(r).Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can delete recordings")
		return
	}
	if a.d.Deletions == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the archive is not available yet")
		return
	}
	var in deletionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sc, ok := in.scope(time.Now())
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "pick at least one camera and a start before the end")
		return
	}
	plan, err := a.d.Deletions.Delete(r.Context(), sc, in.DryRun)
	switch {
	case errors.Is(err, export.ErrRunning):
		writeError(w, http.StatusConflict, "export_running", "an export of this footage is still being made; try again when it has finished")
		return
	case errors.Is(err, retention.ErrInvalidScope):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	case err != nil:
		a.internal(w, err)
		return
	}
	if !in.DryRun {
		a.recordDeletion(r, sc, plan)
	}
	writeJSON(w, http.StatusOK, toDeletionView(plan))
}

// scope turns the request into a deletion scope; the end is never later
// than now.
func (in deletionInput) scope(now time.Time) (retention.Scope, bool) {
	sc := retention.Scope{To: now, Exports: in.IncludeExports}
	if in.Cameras != nil {
		if len(*in.Cameras) == 0 {
			return sc, false
		}
		sc.Cameras = *in.Cameras
	}
	if in.To != nil && in.To.Before(now) {
		sc.To = *in.To
	}
	if in.From != nil {
		sc.From = *in.From
		if !sc.From.Before(sc.To) {
			return sc, false
		}
	}
	return sc, true
}

func (a *api) recordDeletion(r *http.Request, sc retention.Scope, plan retention.Plan) {
	target := "*"
	if sc.Cameras != nil {
		target = strings.Join(sc.Cameras, ",")
	}
	segments, bytes, pending := 0, int64(0), 0
	for _, c := range plan.Cameras {
		segments, bytes, pending = segments+c.Segments, bytes+c.Bytes, pending+c.Pending
	}
	details := map[string]any{"to": sc.To.UTC(), "segments": segments, "bytes": bytes, "pending": pending,
		"exports": plan.Exports.Jobs, "export_bytes": plan.Exports.Bytes}
	if !sc.From.IsZero() {
		details["from"] = sc.From.UTC()
	}
	a.record(r, "archive.delete", target, details)
}

func toDeletionView(p retention.Plan) deletionView {
	v := deletionView{Cameras: make([]cameraDeletionView, 0, len(p.Cameras))}
	for _, c := range p.Cameras {
		v.Cameras = append(v.Cameras, cameraDeletionView{Camera: c.Camera, From: c.From.UTC(), To: c.To.UTC(), Segments: c.Segments, Bytes: c.Bytes, Pending: c.Pending})
	}
	v.Exports.Count, v.Exports.Bytes = p.Exports.Jobs, p.Exports.Bytes
	return v
}
