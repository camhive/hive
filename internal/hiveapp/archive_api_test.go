package hiveapp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// playbackEngine is a fake engine whose PlaybackURL points at an httptest
// server that returns a recognisable fMP4-looking body and records requests.
type playbackEngine struct {
	video.Engine
	srv  *httptest.Server
	last string
	body []byte // what playback answers; nil: a recognisable stub
}

func (p *playbackEngine) PlaybackURL(cam video.CameraID, s video.Stream, start time.Time, d time.Duration) string {
	return p.srv.URL + "/get?path=" + string(cam) + "/" + string(s) + "&start=" + start.UTC().Format(time.RFC3339) + "&duration=" + d.String()
}

func newArchiveAPI(t *testing.T) (*apiFixture, *playbackEngine, camera.Camera) {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := &playbackEngine{}
	eng.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eng.last = r.URL.String()
		w.Header().Set("Content-Type", "video/mp4")
		if eng.body != nil {
			_, _ = w.Write(eng.body)
			return
		}
		_, _ = w.Write([]byte("\x00\x00\x00\x18ftypiso5" + strings.Repeat("v", 100)))
	}))
	t.Cleanup(eng.srv.Close)

	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/door"}}, time.Now())
	_ = st.Cameras().Put(context.Background(), cam)
	idx := archive.NewIndex(st)
	base := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for i := range 3 {
		_ = idx.Upsert(context.Background(), archive.Segment{Camera: string(cam.ID), Stream: "main", Start: base.Add(time.Duration(i) * 10 * time.Minute), Duration: 10 * time.Minute, Bytes: 1, Path: "/x", Complete: true,
			Activity: &archive.Activity{StepS: 10, VideoKbps: []int{100 + i, 900}, SoundDB: []int{-90, -20}}})
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	h := hiveapp.NewAPI(hiveapp.Deps{
		Store: st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}), Version: "test",
		Index: idx, Engine: eng, Exports: export.NewManager(t.TempDir(), hiveapp.PlaybackFetch(eng), st.Exports()), Location: berlin,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}, eng, cam
}

func TestCalendarAndTimelineFromTheIndex(t *testing.T) {
	f, _, cam := newArchiveAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(cam.ID))
	r := f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/calendar?stream=main", viewer, nil)
	if r.Status != 200 || len(r.List) != 1 || r.List[0]["day"] != "2026-11-09" {
		t.Fatalf("calendar: %d %v", r.Status, r.List)
	}
	r = f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/timeline?stream=main&from=2026-11-09T10:05:00Z&to=2026-11-09T10:25:00Z", viewer, nil)
	if r.Status != 200 || len(r.List) != 3 || r.List[0]["duration_ms"] != float64(600000) {
		t.Fatalf("timeline: %d %v", r.Status, r.List)
	}
	// ADR-0015: the activity profile travels with each segment.
	if a, _ := r.List[1]["activity"].(map[string]any); a == nil || a["step_s"] != float64(10) || a["video_kbps"].([]any)[0] != float64(101) || a["sound_db"].([]any)[1] != float64(-20) {
		t.Fatalf("activity: %v", r.List[1]["activity"])
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/timeline?stream=main&from=bad", viewer, nil); r.Status != 400 || r.Body["code"] != "invalid" {
		t.Fatalf("bad range: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "GET", "/v0/cameras/cam_nope/archive/calendar?stream=main", viewer, nil); r.Status != 404 {
		t.Fatalf("ungranted camera: %d", r.Status)
	}
}

func TestPlaybackIsProxiedWithScopeCheck(t *testing.T) {
	f, eng, cam := newArchiveAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(cam.ID))
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/cameras/"+string(cam.ID)+"/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=90", nil)
	req.Header.Set("Authorization", "Bearer "+viewer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp4" || string(body[4:8]) != "ftyp" {
		t.Fatalf("play: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body[:12])
	}
	if !strings.Contains(eng.last, "duration=1m30s") || !strings.Contains(eng.last, string(cam.ID)+"/main") {
		t.Fatalf("engine asked for %s", eng.last)
	}
	other := f.token(t, auth.RoleViewer, "cam_other")
	if r := f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=90", other, nil); r.Status != 404 {
		t.Fatalf("ungranted play: %d", r.Status)
	}
}

func TestExportRequiresOperatorAndProducesADownload(t *testing.T) {
	f, _, cam := newArchiveAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(cam.ID))
	body := map[string]any{"stream": "main", "start": "2026-11-09T10:00:00Z", "duration_seconds": 120}
	if r := f.do(t, "POST", "/v0/cameras/"+string(cam.ID)+"/exports", viewer, body); r.Status != 403 {
		t.Fatalf("viewer export: %d", r.Status)
	}
	operator := f.token(t, auth.RoleOperator, string(cam.ID))
	r := f.do(t, "POST", "/v0/cameras/"+string(cam.ID)+"/exports", operator, body)
	if r.Status != 202 || r.Body["id"] == nil || r.Body["status"] == nil {
		t.Fatalf("create export: %d %v", r.Status, r.Body)
	}
	id := r.Body["id"].(string)

	deadline := time.Now().Add(5 * time.Second)
	for {
		r = f.do(t, "GET", "/v0/exports/"+id, operator, nil)
		if r.Body["status"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("export never finished: %v", r.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r.Body["file_name"] == nil || r.Body["bytes"].(float64) < 100 {
		t.Fatalf("done job = %v", r.Body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/exports/"+id+"/file", nil)
	req.Header.Set("Authorization", "Bearer "+operator)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), ".mp4") || string(data[4:8]) != "ftyp" {
		t.Fatalf("download: %d %s", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	// Another account cannot see or fetch this job.
	now := time.Now()
	stranger, _ := f.iss.Issue(auth.Claims{Account: "acc_other", HiveID: "hv_1", SessionID: "s2", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now, Expiry: now.Add(time.Minute)})
	if r := f.do(t, "GET", "/v0/exports/"+id, stranger, nil); r.Status != 404 {
		t.Fatalf("stranger sees job: %d", r.Status)
	}
	if r := f.do(t, "GET", "/v0/exports", operator, nil); r.Status != 200 || len(r.List) != 1 {
		t.Fatalf("list: %d %v", r.Status, r.List)
	}
	if r := f.do(t, "POST", "/v0/cameras/"+string(cam.ID)+"/exports", operator, map[string]any{"stream": "main", "start": "2026-11-09T10:00:00Z", "duration_seconds": 0}); r.Status != 422 {
		t.Fatalf("invalid export: %d %v", r.Status, r.Body)
	}
}

func TestArchiveEndpointsAreNotReadyWithoutIndex(t *testing.T) {
	f := newAPI(t) // no Index/Engine/Exports
	owner := f.token(t, auth.RoleOwner, "*")
	if r := f.do(t, "GET", "/v0/cameras/cam_x/archive/calendar?stream=main", owner, nil); r.Status != 503 || r.Body["code"] != "not_ready" {
		t.Fatalf("no index: %d %v", r.Status, r.Body)
	}
	_ = json.Marshal
}
