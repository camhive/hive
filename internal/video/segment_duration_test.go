package video

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Encrypting the archive shortens segments (ADR-0019 §2): every path the
// engine records gets the new length, and so do paths added afterwards.
func TestSegmentDurationChangesForEveryPathAndTheNextOnes(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(body))
		mu.Unlock()
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	apiPort, _ := strconv.Atoi(port)
	m := &MediaMTX{
		MediaMTXConfig: MediaMTXConfig{Host: host, APIPort: apiPort, ArchiveDir: "/a", SegmentDuration: 10 * time.Minute},
		client:         srv.Client(), cmd: &exec.Cmd{}, // running, as far as api() can tell
		pulls: map[string]*pullHandle{"cam_2/sub": {}, "cam_1/main": {}},
	}

	if err := m.SetSegmentDuration(context.Background(), 2*time.Minute); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`PATCH /v3/config/paths/patch/cam_1/main {"recordSegmentDuration":"2m0s"}`,
		`PATCH /v3/config/paths/patch/cam_2/sub {"recordSegmentDuration":"2m0s"}`,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if cfg := string(m.pathConfig()); !strings.Contains(cfg, `"recordSegmentDuration":"2m0s"`) {
		t.Fatalf("paths added later get %s", cfg)
	}
}

func TestSegmentDurationOfAStoppedEngineAppliesWhenPathsAreAdded(t *testing.T) {
	m := &MediaMTX{MediaMTXConfig: MediaMTXConfig{ArchiveDir: "/a", SegmentDuration: 10 * time.Minute}, pulls: map[string]*pullHandle{"cam_1/main": {}}}
	if err := m.SetSegmentDuration(context.Background(), 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if cfg := string(m.pathConfig()); !strings.Contains(cfg, `"recordSegmentDuration":"2m0s"`) {
		t.Fatalf("path config %s", cfg)
	}
}
