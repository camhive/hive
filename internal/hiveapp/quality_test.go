package hiveapp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

// streamEngine is a fake engine that answers WHEP offers and playback, and
// records which stream each request asked for.
type streamEngine struct {
	video.Engine
	srv  *httptest.Server
	mu   sync.Mutex
	asks []string // "whep cam/stream" or "play cam/stream"
}

func newStreamEngine(t *testing.T) *streamEngine {
	t.Helper()
	e := &streamEngine{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Location", r.URL.Path+"/sess")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("v=0 answer"))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("\x00\x00\x00\x18ftypiso5"))
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *streamEngine) note(s string) {
	e.mu.Lock()
	e.asks = append(e.asks, s)
	e.mu.Unlock()
}

func (e *streamEngine) last() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.asks) == 0 {
		return ""
	}
	return e.asks[len(e.asks)-1]
}

func (e *streamEngine) WHEPURL(cam video.CameraID, s video.Stream) string {
	e.note("whep " + string(cam) + "/" + string(s))
	return e.srv.URL + "/" + string(cam) + "/" + string(s) + "/whep"
}

func (e *streamEngine) PlaybackURL(cam video.CameraID, s video.Stream, start time.Time, d time.Duration) string {
	e.note("play " + string(cam) + "/" + string(s))
	return e.srv.URL + "/get"
}

type qualityFixture struct {
	direct, relayed *apiFixture
	eng             *streamEngine
	both, mainOnly  camera.Camera
}

// newQualityAPI serves one hive API twice: as the LAN listener sees it and
// as connections piped in by the relay are marked (hiveapp.RelayedConn).
func newQualityAPI(t *testing.T) *qualityFixture {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	both, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/main"},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Stream("sub"): {URL: "rtsp://10.0.0.9/sub"}}}, time.Now())
	mainOnly, _ := camera.New(camera.Spec{Name: "Shed", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.8/only"}}, time.Now())
	for _, c := range []camera.Camera{both, mainOnly} {
		if err := st.Cameras().Put(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	eng := newStreamEngine(t)
	h := hiveapp.NewAPI(hiveapp.Deps{
		Store: st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}), Version: "test", Notify: func() {},
		Engine: eng, Index: archive.NewIndex(st), Exports: export.NewManager(t.TempDir(), hiveapp.PlaybackFetch(eng), st.Exports()),
	})
	direct := httptest.NewServer(h)
	t.Cleanup(direct.Close)
	relayed := httptest.NewUnstartedServer(h)
	relayed.Config.ConnContext = hiveapp.RelayedConn
	relayed.Start()
	t.Cleanup(relayed.Close)
	return &qualityFixture{direct: &apiFixture{srv: direct, iss: iss, st: st}, relayed: &apiFixture{srv: relayed, iss: iss, st: st}, eng: eng, both: both, mainOnly: mainOnly}
}

// freeToken is what the cloud issues on a plan with remote.quality=sub.
func (q *qualityFixture) freeToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	tok, err := q.direct.iss.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "sid", Cameras: []string{"*"}, Role: auth.RoleOperator,
		IssuedAt: now, Expiry: now.Add(5 * time.Minute), Quality: "sub", ExportMaxMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func play(t *testing.T, f *apiFixture, tok string, cam camera.Camera) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/cameras/"+string(cam.ID)+"/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=60", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// #58: the token's quality claim binds remote requests (through the relay)
// whatever stream the client asks for; on the LAN the plan never limits
// viewing or export (recording and LAN use do not depend on the plan).
func TestRemoteRequestsGetThePlanQualityLANRequestsDoNot(t *testing.T) {
	q := newQualityAPI(t)
	free := q.freeToken(t)

	// Archive playback.
	if s := play(t, q.relayed, free, q.both); s != 200 || q.eng.last() != "play "+string(q.both.ID)+"/sub" {
		t.Fatalf("relayed play: %d %s", s, q.eng.last())
	}
	if s := play(t, q.relayed, free, q.mainOnly); s != 200 || q.eng.last() != "play "+string(q.mainOnly.ID)+"/main" {
		t.Fatalf("relayed play, camera without sub: %d %s", s, q.eng.last())
	}
	if s := play(t, q.direct, free, q.both); s != 200 || q.eng.last() != "play "+string(q.both.ID)+"/main" {
		t.Fatalf("LAN play: %d %s", s, q.eng.last())
	}

	// Live offer on the hive API.
	offer := map[string]string{"stream": "main", "sdp": "v=0 offer"}
	if r := q.relayed.do(t, "POST", "/v0/cameras/"+string(q.both.ID)+"/live/offer", free, offer); r.Status != 201 || q.eng.last() != "whep "+string(q.both.ID)+"/sub" {
		t.Fatalf("relayed live: %d %v %s", r.Status, r.Body, q.eng.last())
	}
	if r := q.direct.do(t, "POST", "/v0/cameras/"+string(q.both.ID)+"/live/offer", free, offer); r.Status != 201 || q.eng.last() != "whep "+string(q.both.ID)+"/main" {
		t.Fatalf("LAN live: %d %v %s", r.Status, r.Body, q.eng.last())
	}

	// Exports: the stream and the length cap apply remotely only.
	long := map[string]any{"stream": "main", "start": "2026-11-09T10:00:00Z", "duration_seconds": 600}
	short := map[string]any{"stream": "main", "start": "2026-11-09T10:00:00Z", "duration_seconds": 60}
	if r := q.relayed.do(t, "POST", "/v0/cameras/"+string(q.both.ID)+"/exports", free, long); r.Status != 403 || r.Body["code"] != "plan_limit" {
		t.Fatalf("relayed export over the cap: %d %v", r.Status, r.Body)
	}
	if r := q.relayed.do(t, "POST", "/v0/cameras/"+string(q.both.ID)+"/exports", free, short); r.Status != 202 || r.Body["stream"] != "sub" {
		t.Fatalf("relayed export: %d %v", r.Status, r.Body)
	}
	if r := q.direct.do(t, "POST", "/v0/cameras/"+string(q.both.ID)+"/exports", free, long); r.Status != 202 || r.Body["stream"] != "main" {
		t.Fatalf("LAN export is not limited by plan: %d %v", r.Status, r.Body)
	}
}

// Full-quality tokens keep the requested stream on every path.
func TestFullQualityTokenKeepsTheRequestedStream(t *testing.T) {
	q := newQualityAPI(t)
	now := time.Now()
	full, _ := q.direct.iss.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "sid", Cameras: []string{"*"}, Role: auth.RoleViewer,
		IssuedAt: now, Expiry: now.Add(5 * time.Minute), Quality: "full"})
	if s := play(t, q.relayed, full, q.both); s != 200 || !strings.HasSuffix(q.eng.last(), "/main") {
		t.Fatalf("relayed play with full quality: %d %s", s, q.eng.last())
	}
}

// Playback spans are 1 to 86400 seconds.
func TestPlaybackSpanLimits(t *testing.T) {
	q := newQualityAPI(t)
	tok := q.freeToken(t)
	for _, c := range []struct {
		duration string
		status   int
	}{{"0", 400}, {"1", 200}, {"86400", 200}, {"86401", 400}, {"x", 400}} {
		path := "/v0/cameras/" + string(q.both.ID) + "/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=" + c.duration
		req, _ := http.NewRequestWithContext(context.Background(), "GET", q.direct.srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("duration %s: %d, want %d", c.duration, resp.StatusCode, c.status)
		}
	}
}

// The archive a remote viewer sees is the archive they can play: through the
// relay calendar, timeline and playback all answer for the served stream, and
// playback names it (X-Camhive-Stream) so the player picks that stream's
// codec. Without this a Free viewer saw main's coverage, got sub's bytes
// decoded as main's codec, and the player sat at 0:00.
func TestRemoteArchiveAnswersForTheServedStream(t *testing.T) {
	q := newQualityAPI(t)
	free := q.freeToken(t)
	idx := archive.NewIndex(q.direct.st)
	base := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	_ = idx.Upsert(context.Background(), archive.Segment{Camera: string(q.both.ID), Stream: "main", Start: base, Duration: 10 * time.Minute, Bytes: 1, Path: "/m", Complete: true})
	_ = idx.Upsert(context.Background(), archive.Segment{Camera: string(q.both.ID), Stream: "sub", Start: base.Add(30 * time.Minute), Duration: 10 * time.Minute, Bytes: 1, Path: "/s", Complete: true})
	timeline := "/v0/cameras/" + string(q.both.ID) + "/archive/timeline?stream=main&from=2026-11-09T09:00:00Z&to=2026-11-09T12:00:00Z"

	if r := q.relayed.do(t, "GET", timeline, free, nil); r.Status != 200 || len(r.List) != 1 || r.List[0]["start"] != "2026-11-09T10:30:00Z" {
		t.Fatalf("relayed timeline shows sub: %d %v", r.Status, r.List)
	}
	if r := q.direct.do(t, "GET", timeline, free, nil); r.Status != 200 || len(r.List) != 1 || r.List[0]["start"] != "2026-11-09T10:00:00Z" {
		t.Fatalf("LAN timeline shows main: %d %v", r.Status, r.List)
	}
	cal := "/v0/cameras/" + string(q.both.ID) + "/archive/calendar?stream=main"
	if r := q.relayed.do(t, "GET", cal, free, nil); r.Status != 200 || len(r.List) != 1 {
		t.Fatalf("relayed calendar: %d %v", r.Status, r.List)
	}

	for _, c := range []struct {
		name string
		f    *apiFixture
		cam  camera.Camera
		want string
	}{
		{"relayed", q.relayed, q.both, "sub"},
		{"relayed, camera without sub", q.relayed, q.mainOnly, "main"},
		{"LAN", q.direct, q.both, "main"},
	} {
		if got := servedHeader(t, c.f, free, c.cam); got != c.want {
			t.Errorf("%s: X-Camhive-Stream = %q, want %q", c.name, got, c.want)
		}
	}
	// A browser on another origin may read it.
	req, _ := http.NewRequestWithContext(context.Background(), "GET", q.direct.srv.URL+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), "X-Camhive-Stream") {
		t.Fatalf("expose headers: %q", resp.Header.Get("Access-Control-Expose-Headers"))
	}
}

func servedHeader(t *testing.T, f *apiFixture, tok string, cam camera.Camera) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/cameras/"+string(cam.ID)+"/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=60", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.Header.Get("X-Camhive-Stream")
}
