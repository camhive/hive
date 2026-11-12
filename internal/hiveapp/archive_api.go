package hiveapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/video"
)

// PlaybackFetch adapts the engine's playback endpoint to export.Fetch.
func PlaybackFetch(engine video.Engine) export.Fetch {
	client := &http.Client{} // no timeout: a long export streams for a while
	return func(ctx context.Context, req export.Request) (io.ReadCloser, error) {
		u := engine.PlaybackURL(video.CameraID(req.Camera), video.Stream(req.Stream), req.Start, req.Duration)
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(r)
		if err != nil {
			return nil, fmt.Errorf("playback: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("playback: engine answered %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}

func (a *api) requireArchive(w http.ResponseWriter) bool {
	if a.d.Index == nil || a.d.Engine == nil || a.d.Exports == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "archive is not initialised")
		return false
	}
	return true
}

// cameraScope checks the token covers the camera for action; ungranted
// cameras look absent.
func cameraScope(w http.ResponseWriter, r *http.Request, action auth.Action) (string, bool) {
	id := r.PathValue("id")
	claims := claimsFrom(r)
	if !claims.Allows(id, auth.ActionLive) {
		writeError(w, http.StatusNotFound, "not_found", "camera not found")
		return "", false
	}
	if !claims.Allows(id, action) {
		writeError(w, http.StatusForbidden, "forbidden", "your role does not allow "+string(action))
		return "", false
	}
	return id, true
}

func streamParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := r.URL.Query().Get("stream")
	if s == "" {
		s = "main"
	}
	if s != "main" && s != "sub" {
		writeError(w, http.StatusBadRequest, "invalid", "stream must be main or sub")
		return "", false
	}
	return s, true
}

func (a *api) calendar(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionArchive)
	if !ok {
		return
	}
	stream, ok := a.archiveStream(w, r, id)
	if !ok {
		return
	}
	loc := a.d.Location
	if tz := r.URL.Query().Get("tz"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "unknown tz")
			return
		}
		loc = l
	}
	days, err := a.d.Index.Calendar(r.Context(), id, stream, loc)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]map[string]string, 0, len(days))
	for _, d := range days {
		out = append(out, map[string]string{"day": d.Format("2006-01-02")})
	}
	writeJSON(w, http.StatusOK, out)
}

type segmentView struct {
	Start      time.Time         `json:"start"`
	DurationMS int64             `json:"duration_ms"`
	Bytes      int64             `json:"bytes"`
	Complete   bool              `json:"complete"`
	Activity   *archive.Activity `json:"activity,omitempty"`
}

func (a *api) timeline(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionArchive)
	if !ok {
		return
	}
	stream, ok := a.archiveStream(w, r, id)
	if !ok {
		return
	}
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		writeError(w, http.StatusBadRequest, "invalid", "from and to must be RFC 3339 with to > from")
		return
	}
	segs, err := a.d.Index.Range(r.Context(), id, stream, from, to)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]segmentView, 0, len(segs))
	for _, s := range segs {
		out = append(out, segmentView{Start: s.Start, DurationMS: s.Duration.Milliseconds(), Bytes: s.Bytes, Complete: s.Complete, Activity: s.Activity})
	}
	writeJSON(w, http.StatusOK, out)
}

// servedStream is the stream a request gets: through the relay the token's
// quality decides (#58), on the LAN the plan does not limit anything; either
// way a camera without the stream falls back to the one it has (nil camera:
// no store, the request stands).
func servedStream(r *http.Request, cam *camera.Camera, want string) string {
	if Relayed(r.Context()) {
		want = auth.RemoteStream(claimsFrom(r).Quality, want)
	}
	return string(hiveagent.ResolveStream(cam, want))
}

// servedStreamHeader names the stream an archive answer is for.
const servedStreamHeader = "X-Camhive-Stream"

// archiveStream is the stream calendar, timeline and playback answer for:
// the requested one as served (servedStream), so a remote viewer sees the
// coverage of the footage they can play.
func (a *api) archiveStream(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	want, ok := streamParam(w, r)
	if !ok {
		return "", false
	}
	cam, _ := a.camera(r, id)
	return servedStream(r, cam, want), true
}

// liveCamera is the camera a live offer may use: known and switched on.
// Without a store there is nothing to check and the engine decides.
func (a *api) liveCamera(r *http.Request, id string) (*camera.Camera, bool) {
	if a.d.Store == nil {
		return nil, true
	}
	cam, found := a.camera(r, id)
	return cam, found && cam.Enabled
}

// camera looks the camera up; nil and false without a store or when it is unknown.
func (a *api) camera(r *http.Request, id string) (*camera.Camera, bool) {
	if a.d.Store == nil {
		return nil, false
	}
	c, err := a.d.Store.Cameras().Get(r.Context(), camera.ID(id))
	if err != nil {
		return nil, false
	}
	return &c, true
}

// playSpan reads start (RFC 3339) and duration (1..86400 seconds).
func playSpan(w http.ResponseWriter, r *http.Request) (time.Time, time.Duration, bool) {
	start, err := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	secs, err2 := strconv.Atoi(r.URL.Query().Get("duration"))
	if err != nil || err2 != nil || secs <= 0 || secs > 24*3600 {
		writeError(w, http.StatusBadRequest, "invalid", "start (RFC 3339) and duration (1..86400 seconds) are required")
		return time.Time{}, 0, false
	}
	return start, time.Duration(secs) * time.Second, true
}

// play proxies the engine's playback stream for a span. The engine listens
// on loopback only; this is the sole path to it, and it carries the scope.
func (a *api) play(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionArchive)
	if !ok {
		return
	}
	stream, ok := a.archiveStream(w, r, id)
	if !ok {
		return
	}
	start, span, ok := playSpan(w, r)
	if !ok {
		return
	}
	body, err := PlaybackFetch(a.d.Engine)(r.Context(), export.Request{Camera: id, Stream: stream, Start: start, Duration: span, Owner: claimsFrom(r).Account})
	if err != nil {
		writeError(w, http.StatusBadGateway, "engine", "playback unavailable")
		return
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set(servedStreamHeader, stream) // the player needs this stream's codec, not the one it asked for
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = archive.CopyPlayable(w, body) // video only: browsers refuse the whole file over a track they cannot decode
}

type exportInput struct {
	Stream          string    `json:"stream"`
	Start           time.Time `json:"start"`
	DurationSeconds int       `json:"duration_seconds"`
}

type exportView struct {
	ID        string    `json:"id"`
	Camera    string    `json:"camera"`
	Stream    string    `json:"stream"`
	Start     time.Time `json:"start"`
	Duration  int       `json:"duration_seconds"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Bytes     int64     `json:"bytes"`
	FileName  string    `json:"file_name"`
	CreatedAt time.Time `json:"created_at"`
}

func toExportView(j export.Job) exportView {
	return exportView{
		ID: j.ID, Camera: j.Request.Camera, Stream: j.Request.Stream, Start: j.Request.Start,
		Duration: int(j.Request.Duration.Seconds()), Status: string(j.Status), Error: j.Error,
		Bytes: j.Bytes, FileName: j.FileName, CreatedAt: j.CreatedAt,
	}
}

func (a *api) createExport(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	id, ok := cameraScope(w, r, auth.ActionExport)
	if !ok {
		return
	}
	var in exportInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Stream == "" {
		in.Stream = "main"
	}
	cam, _ := a.camera(r, id)
	in.Stream = servedStream(r, cam, in.Stream)
	if claims := claimsFrom(r); Relayed(r.Context()) && !claims.ExportAllowed(time.Duration(in.DurationSeconds)*time.Second) {
		writeError(w, http.StatusForbidden, "plan_limit", fmt.Sprintf("your plan allows exports up to %d minutes", claims.ExportMaxMinutes))
		return
	}
	job, err := a.d.Exports.Start(r.Context(), export.Request{
		Camera: id, Stream: in.Stream, Start: in.Start, Duration: time.Duration(in.DurationSeconds) * time.Second,
		Owner: claimsFrom(r).Account,
	})
	if errors.Is(err, export.ErrInvalid) {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toExportView(job))
}

// ownJob returns the job if it belongs to the caller; otherwise it looks absent.
func (a *api) ownJob(w http.ResponseWriter, r *http.Request) (export.Job, bool) {
	job, err := a.d.Exports.Get(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, export.ErrNotFound) {
		a.internal(w, err)
		return export.Job{}, false
	}
	if err != nil || job.Request.Owner != claimsFrom(r).Account {
		writeError(w, http.StatusNotFound, "not_found", "export not found")
		return export.Job{}, false
	}
	return job, true
}

func (a *api) getExport(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	if job, ok := a.ownJob(w, r); ok {
		writeJSON(w, http.StatusOK, toExportView(job))
	}
}

func (a *api) listExports(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	jobs, err := a.d.Exports.List(r.Context(), claimsFrom(r).Account)
	if err != nil {
		a.internal(w, err)
		return
	}
	out := make([]exportView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toExportView(j))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) exportFile(w http.ResponseWriter, r *http.Request) {
	if !a.requireArchive(w) {
		return
	}
	job, ok := a.ownJob(w, r)
	if !ok {
		return
	}
	path, err := a.d.Exports.FilePath(r.Context(), job.ID)
	if errors.Is(err, export.ErrNotReady) {
		writeError(w, http.StatusConflict, "not_ready", "export is not finished")
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="`+job.FileName+`"`)
	http.ServeFile(w, r, path)
}

// archiveIndexer is the subset of archive.Index the API uses.
type archiveIndexer interface {
	Calendar(ctx context.Context, camera, stream string, loc *time.Location) ([]time.Time, error)
	Range(ctx context.Context, camera, stream string, from, to time.Time) ([]archive.Segment, error)
}
