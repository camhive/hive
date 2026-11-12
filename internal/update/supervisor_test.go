package update_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/update"
)

// fakeHives runs "versions" as fake processes; a version listed in broken
// starts but never becomes healthy, one in crashing exits at once.
type fakeHives struct {
	mu       sync.Mutex
	started  []string
	running  string
	broken   map[string]bool
	crashing map[string]bool
	stopped  int
	last     *fakeProc
}

// crash makes the running fake hive exit.
func (h *fakeHives) crash() {
	h.mu.Lock()
	p := h.last
	h.running = ""
	h.mu.Unlock()
	p.exit <- errors.New("killed")
}

type fakeProc struct {
	h    *fakeHives
	ver  string
	exit chan error
}

func (p *fakeProc) Wait() error { return <-p.exit }
func (p *fakeProc) Stop() error {
	p.h.mu.Lock()
	p.h.stopped++
	if p.h.running == p.ver {
		p.h.running = ""
	}
	p.h.mu.Unlock()
	select {
	case p.exit <- nil:
	default:
	}
	return nil
}

func (h *fakeHives) Start(_ context.Context, v update.Version, _ string) (update.Process, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.started = append(h.started, v.String())
	p := &fakeProc{h: h, ver: v.String(), exit: make(chan error, 1)}
	h.last = p
	if h.crashing[v.String()] {
		p.exit <- errors.New("crashed")
		return p, nil
	}
	h.running = v.String()
	return p, nil
}

func (h *fakeHives) Health(context.Context) (update.Health, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running == "" || h.broken[h.running] {
		return update.Health{}, errors.New("no answer")
	}
	v, _ := update.ParseVersion(h.running)
	return update.Health{Version: v, Recording: 2}, nil
}

func (h *fakeHives) runningVersion() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// fakeSource offers one bundle and writes its files when downloaded.
type fakeSource struct {
	target update.Target
	err    error
}

func (f *fakeSource) Check(context.Context) (update.Target, error) { return f.target, f.err }
func (f *fakeSource) Fetch(_ context.Context, dst string) error {
	return writeBundle(dst, f.target.Version.String())
}

func writeBundle(dst, tag string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	for _, n := range update.BundleFiles {
		if err := os.WriteFile(filepath.Join(dst, n), []byte(tag), 0o755); err != nil { //nolint:gosec // test executables
			return err
		}
	}
	return nil
}

func supervisor(t *testing.T, hives *fakeHives, src *fakeSource) (*update.Supervisor, *update.Versions, func() []string) {
	t.Helper()
	return supervisorWith(t, hives, src, nil)
}

func supervisorWith(t *testing.T, hives *fakeHives, src update.Channel, with func(*update.SupervisorConfig)) (*update.Supervisor, *update.Versions, func() []string) {
	t.Helper()
	dir := t.TempDir()
	vs := update.OpenVersions(dir)
	seed := t.TempDir()
	if err := writeBundle(seed, "0.1.4"); err != nil {
		t.Fatal(err)
	}
	if err := vs.Seed(v(t, "0.1.4"), seed); err != nil {
		t.Fatal(err)
	}
	var events []string
	var mu sync.Mutex
	w, _ := update.ParseWindow("00:00-23:59")
	cfg := update.SupervisorConfig{
		Versions: vs, Runner: hives, Health: hives.Health, Source: src,
		Hive: "hv_1", Updater: v(t, "0.1.4"), Window: w, Mode: update.Automatic,
		CheckEvery: time.Hour, HealthTimeout: 300 * time.Millisecond, Poll: 10 * time.Millisecond, Restart: 10 * time.Millisecond,
		Report: func(kind string, data map[string]any) { mu.Lock(); events = append(events, kind); mu.Unlock() },
	}
	if with != nil {
		with(&cfg)
	}
	s := update.NewSupervisor(cfg)
	return s, vs, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), events...) }
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHealthyUpdateIsCommitted(t *testing.T) {
	hives := &fakeHives{}
	s, vs, events := supervisor(t, hives, &fakeSource{target: update.Target{Version: v(t, "0.1.5"), Rollout: 100}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return hives.runningVersion() == "0.1.5" })
	eventually(t, func() bool { cur, _ := vs.Current(); return cur.String() == "0.1.5" && len(events()) > 0 })
	if events()[0] != "update.applied" {
		t.Fatalf("events = %v", events())
	}
}

// ADR-0005/0018: a version that does not become healthy in time is rolled
// back, marked failed and not tried again.
func TestUnhealthyUpdateRollsBackAndIsNotRetried(t *testing.T) {
	hives := &fakeHives{broken: map[string]bool{"0.1.5": true}}
	s, vs, events := supervisor(t, hives, &fakeSource{target: update.Target{Version: v(t, "0.1.5"), Rollout: 100}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return len(events()) > 0 })
	eventually(t, func() bool { return hives.runningVersion() == "0.1.4" })
	if cur, _ := vs.Current(); cur.String() != "0.1.4" || events()[0] != "update.rolled_back" {
		t.Fatalf("current %v events %v", cur, events())
	}
	if f := vs.Failed(); len(f) != 1 || f[0].String() != "0.1.5" {
		t.Fatalf("failed = %v", f)
	}
	s.CheckNow(ctx)
	if n := count(hives, "0.1.5"); n != 1 {
		t.Fatalf("0.1.5 started %d times", n)
	}
}

// A version that keeps crashing right after the switch rolls back too.
func TestCrashingUpdateRollsBack(t *testing.T) {
	hives := &fakeHives{crashing: map[string]bool{"0.1.5": true}}
	s, vs, events := supervisor(t, hives, &fakeSource{target: update.Target{Version: v(t, "0.1.5"), Rollout: 100}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return len(events()) > 0 && hives.runningVersion() == "0.1.4" })
	if cur, _ := vs.Current(); cur.String() != "0.1.4" {
		t.Fatalf("current %v", cur)
	}
}

// The running hive is restarted when it dies, without any update.
func TestCrashedHiveIsRestarted(t *testing.T) {
	hives := &fakeHives{}
	s, _, _ := supervisor(t, hives, &fakeSource{err: errors.New("offline")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	eventually(t, func() bool { return hives.runningVersion() == "0.1.4" })
	hives.crash()
	eventually(t, func() bool { return count(hives, "0.1.4") >= 2 })
	cancel()
}

func count(h *fakeHives, ver string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.started {
		if s == ver {
			n++
		}
	}
	return n
}
