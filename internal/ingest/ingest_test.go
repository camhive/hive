package ingest_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/ingest"
	"github.com/camhive/hive/internal/video"
)

// fakeEngine records sources and can simulate a crash.
type fakeEngine struct {
	mu       sync.Mutex
	started  int
	sources  map[string]video.Source
	exited   chan struct{}
	failNext bool
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{sources: map[string]video.Source{}, exited: make(chan struct{})}
}

func (f *fakeEngine) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	f.sources = map[string]video.Source{} // a fresh process knows nothing
	f.exited = make(chan struct{})
	return nil
}
func (f *fakeEngine) Stop() error { return nil }
func (f *fakeEngine) AddSource(_ context.Context, s video.Source) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errors.New("boom")
	}
	f.sources[string(s.Camera)+"/"+string(s.Stream)] = s
	return nil
}
func (f *fakeEngine) RemoveSource(_ context.Context, c video.CameraID, s video.Stream) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sources, string(c)+"/"+string(s))
	return nil
}
func (f *fakeEngine) Status(context.Context, video.CameraID, video.Stream) (video.Status, error) {
	return video.Status{Ready: true}, nil
}
func (f *fakeEngine) Segments(context.Context, video.CameraID, video.Stream) ([]video.Segment, error) {
	return nil, nil
}
func (f *fakeEngine) PlaybackURL(video.CameraID, video.Stream, time.Time, time.Duration) string {
	return ""
}
func (f *fakeEngine) WHEPURL(video.CameraID, video.Stream) string { return "" }
func (f *fakeEngine) Exited() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}
func (f *fakeEngine) crash() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.exited)
}
func (f *fakeEngine) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sources))
	for k := range f.sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func (f *fakeEngine) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// memStore is a camera list the test controls.
type memStore struct {
	mu   sync.Mutex
	cams []camera.Camera
}

func (m *memStore) List(context.Context) ([]camera.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]camera.Camera(nil), m.cams...), nil
}
func (m *memStore) set(cams ...camera.Camera) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cams = cams
}

func cam(t *testing.T, name string, enabled bool, withSub bool) camera.Camera {
	t.Helper()
	streams := map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://10.0.0.1/" + name + "/main"}}
	if withSub {
		streams[camera.Sub] = camera.StreamSpec{URL: "rtsp://10.0.0.1/" + name + "/sub"}
	}
	c, err := camera.New(camera.Spec{
		Name:        name,
		Source:      camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.1/" + name + "/main"},
		Credentials: camera.Credentials{Username: "u", Password: "p@ss word"},
		Streams:     streams,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		e := false
		c, _ = c.Update(camera.Patch{Enabled: &e}, time.Now())
	}
	return c
}

func TestReconcileAddsEnabledStreamsWithCredentialsAndRemovesDisabled(t *testing.T) {
	eng := newFakeEngine()
	st := &memStore{}
	a, b := cam(t, "a", true, true), cam(t, "b", false, true)
	st.set(a, b)
	ing := ingest.New(st, eng, ingest.Options{})

	ctx := context.Background()
	if err := ing.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{string(a.ID) + "/main", string(a.ID) + "/sub"}
	if got := eng.keys(); !equal(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	src := eng.sources[string(a.ID)+"/main"]
	if src.URL != "rtsp://u:p%40ss%20word@10.0.0.1/a/main" {
		t.Fatalf("credentials must be injected into the engine URL, got %q", src.URL)
	}

	// Disable a, enable b: sources follow.
	e := false
	a2, _ := a.Update(camera.Patch{Enabled: &e}, time.Now())
	e = true
	b2, _ := b.Update(camera.Patch{Enabled: &e}, time.Now())
	st.set(a2, b2)
	if err := ing.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	want = []string{string(b.ID) + "/main", string(b.ID) + "/sub"}
	if got := eng.keys(); !equal(got, want) {
		t.Fatalf("after change sources = %v, want %v", got, want)
	}
}

func TestReconcileSkipsCamerasWithoutStreamsAndIsIdempotent(t *testing.T) {
	eng := newFakeEngine()
	st := &memStore{}
	c, _ := camera.New(camera.Spec{Name: "pending", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://10.0.0.2/onvif/device_service"}}, time.Now())
	st.set(c, cam(t, "x", true, false))
	ing := ingest.New(st, eng, ingest.Options{})
	for range 3 {
		if err := ing.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := eng.keys(); len(got) != 1 {
		t.Fatalf("sources = %v, want one (x/main)", got)
	}
}

func TestReconcileReportsEngineErrorsButContinues(t *testing.T) {
	eng := newFakeEngine()
	eng.failNext = true
	st := &memStore{}
	st.set(cam(t, "a", true, false), cam(t, "b", true, false))
	ing := ingest.New(st, eng, ingest.Options{})
	err := ing.Reconcile(context.Background())
	if err == nil {
		t.Fatal("an AddSource failure must be reported")
	}
	if got := eng.keys(); len(got) != 1 {
		t.Fatalf("the other camera must still be added, got %v", got)
	}
	// Next pass heals the failed one.
	if err := ing.Reconcile(context.Background()); err != nil || len(eng.keys()) != 2 {
		t.Fatalf("heal: err=%v sources=%v", err, eng.keys())
	}
}

func TestRunRestartsEngineAfterCrashAndReaddsSources(t *testing.T) {
	eng := newFakeEngine()
	st := &memStore{}
	st.set(cam(t, "a", true, true))
	ing := ingest.New(st, eng, ingest.Options{Interval: time.Hour, RestartBackoff: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ing.Run(ctx) }()

	waitUntil(t, func() bool { return len(eng.keys()) == 2 }, "initial sources")
	if eng.startCount() != 1 {
		t.Fatalf("engine started %d times before crash", eng.startCount())
	}
	eng.crash()
	waitUntil(t, func() bool { return eng.startCount() == 2 && len(eng.keys()) == 2 }, "restart and re-add")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestNotifyTriggersImmediateReconcile(t *testing.T) {
	eng := newFakeEngine()
	st := &memStore{}
	ing := ingest.New(st, eng, ingest.Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ing.Run(ctx) }()
	waitUntil(t, func() bool { return eng.startCount() == 1 }, "engine start")

	st.set(cam(t, "late", true, false))
	ing.Notify()
	waitUntil(t, func() bool { return len(eng.keys()) == 1 }, "source added after Notify")
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
