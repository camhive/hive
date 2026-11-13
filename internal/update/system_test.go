package update_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/camhive/hive/internal/update"
)

// TestMain lets the test binary act as a fake hive child: with
// UPDATE_TEST_CHILD set it waits for SIGTERM and exits 0 (or ignores it).
func TestMain(m *testing.M) {
	switch os.Getenv("UPDATE_TEST_CHILD") {
	case "graceful":
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM)
		fmt.Println("ready")
		<-c
		os.Exit(0)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExecRunnerStopsTheHiveGentlyThenFirmly(t *testing.T) {
	exe, _ := os.Executable()
	for _, mode := range []string{"graceful", "stubborn"} {
		t.Setenv("UPDATE_TEST_CHILD", mode)
		ready := &readyWriter{ch: make(chan struct{})}
		grace := 5 * time.Second // a race-detector child sleeps a second before it exits
		if mode == "stubborn" {
			grace = 200 * time.Millisecond
		}
		r := update.ExecRunner{Args: []string{"-test.run=^$"}, Grace: grace, Stdout: ready}
		p, err := r.Start(context.Background(), update.Version{}, exe)
		if err != nil {
			t.Fatal(err)
		}
		select { // its signal handling is in place
		case <-ready.ch:
		case <-time.After(10 * time.Second):
			t.Fatal("child never got ready")
		}
		start := time.Now()
		_ = p.Stop()
		err = p.Wait()
		if mode == "graceful" && err != nil {
			t.Fatalf("graceful child: %v", err)
		}
		if mode == "stubborn" && (err == nil || time.Since(start) > 5*time.Second) {
			t.Fatalf("stubborn child: %v after %v", err, time.Since(start))
		}
	}
}

// readyWriter closes ch when the child prints "ready".
type readyWriter struct {
	once sync.Once
	ch   chan struct{}
}

func (w *readyWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "ready") {
		w.once.Do(func() { close(w.ch) })
	}
	return len(p), nil
}

func TestHTTPHealthReadsVersionAndRecording(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.1.5","recording":3}`))
	}))
	defer srv.Close()
	h, err := update.HTTPHealth(srv.URL, nil)(context.Background())
	if err != nil || h.Version.String() != "0.1.5" || h.Recording != 3 {
		t.Fatalf("health = %+v, %v", h, err)
	}
	// The hive prints its version with build details, as `hive --version` does.
	real := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.1.90 (abc1234, 2026-11-13)","recording":0}`))
	}))
	defer real.Close()
	if h, err := update.HTTPHealth(real.URL, nil)(context.Background()); err != nil || h.Version.String() != "0.1.90" {
		t.Fatalf("hive's own format: %+v, %v", h, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if _, err := update.HTTPHealth(bad.URL, nil)(context.Background()); err == nil {
		t.Fatal("503 counted as healthy")
	}
	if _, err := update.HTTPHealth("http://127.0.0.1:1", nil)(context.Background()); err == nil {
		t.Fatal("no answer counted as healthy")
	}
}

func TestSourceChannelFetchesWhatItOffered(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	r.release(t, "linux_arm64", "0.1.5", tarGz(t, "v5"), nil)
	c := &update.SourceChannel{Source: r.source(t), Platform: "linux_arm64"}
	if err := c.Fetch(ctx, t.TempDir()); err == nil {
		t.Fatal("fetched before anything was offered")
	}
	target, err := c.Check(ctx)
	if err != nil || target.Version.String() != "0.1.5" {
		t.Fatalf("check = %+v, %v", target, err)
	}
	if err := c.Fetch(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if update.Platform() == "" {
		t.Fatal("no platform")
	}
}
