package update_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/update"
)

// buildHive compiles the real hive with a release version.
func buildHive(t *testing.T, dir, ver string) {
	t.Helper()
	ld := "-X github.com/camhive/hive/internal/version.version=" + ver + " -X github.com/camhive/hive/internal/version.date=2026-11-13"
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", filepath.Join(dir, "hive"), "github.com/camhive/hive/cmd/hive")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build hive %s: %v\n%s", ver, err, out)
	}
}

// standIns writes a mediamtx that never serves (the engine is not under
// test) and an updater placeholder.
func standIns(t *testing.T, dir string) {
	t.Helper()
	for name, body := range map[string]string{"mediamtx": "#!/bin/sh\nexec sleep 3600\n", "hive-updater": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil { //nolint:gosec // test executables
			t.Fatal(err)
		}
	}
}

func tarDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range update.BundleFiles {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		_ = tw.WriteHeader(&tar.Header{Name: "bundle/" + n, Mode: 0o755, Size: int64(len(data))})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// ADR-0005/0018 done-when: a real hive updates over signed metadata, and a
// broken build rolls back on its own, within the health timeout.
func TestRealHiveUpdatesAndABrokenBuildRollsBack(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("integration test (builds the hive; shell stand-ins)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	port := testports.Get(t, 1)[0]
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "hive.yaml"), []byte("listen_addr: 127.0.0.1:"+strconv.Itoa(port)+"\ntls_listen_addr: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMHIVE_DATA_DIR", data)

	v90, v91, v92 := t.TempDir(), t.TempDir(), t.TempDir()
	buildHive(t, v90, "0.1.90")
	buildHive(t, v91, "0.1.91")
	standIns(t, v90)
	standIns(t, v91)
	standIns(t, v92)
	if err := os.WriteFile(filepath.Join(v92, "hive"), []byte("#!/bin/sh\necho broken >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}

	store := t.TempDir()
	versions := update.OpenVersions(store)
	if err := versions.Seed(v(t, "0.1.90"), v90); err != nil {
		t.Fatal(err)
	}
	repo := newRepo(t)
	platform := update.Platform()
	repo.release(t, platform, "0.1.91", tarDir(t, v91), nil)

	var mu sync.Mutex
	var events []string
	all, _ := update.ParseWindow("00:00-23:59")
	sup := update.NewSupervisor(update.SupervisorConfig{
		Versions: versions, Runner: update.ExecRunner{Stdout: io.Discard, Stderr: io.Discard, Grace: 10 * time.Second},
		Health: update.HTTPHealth("http://127.0.0.1:"+strconv.Itoa(port), &http.Client{Timeout: time.Second}),
		Source: &update.SourceChannel{Source: repo.source(t), Platform: platform},
		Hive:   "hv_e2e", Updater: v(t, "0.1.90"), Window: all, Mode: update.Automatic,
		CheckEvery: time.Hour, HealthTimeout: 60 * time.Second, Poll: 250 * time.Millisecond, Restart: time.Second,
		Report: func(kind string, _ map[string]any) { mu.Lock(); events = append(events, kind); mu.Unlock() },
	})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { sup.Run(runCtx); close(done) }()
	defer func() { stop(); <-done }()

	health := update.HTTPHealth("http://127.0.0.1:"+strconv.Itoa(port), &http.Client{Timeout: time.Second})
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for !cond() {
			if ctx.Err() != nil {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("%s: timed out; events %v", what, events)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	running := func(ver string) func() bool {
		return func() bool { h, err := health(ctx); return err == nil && h.Version.String() == ver }
	}
	waitFor("0.1.91 applied", running("0.1.91"))
	waitFor("applied reported", func() bool { mu.Lock(); defer mu.Unlock(); return len(events) == 1 && events[0] == "update.applied" })

	repo.release(t, platform, "0.1.92", tarDir(t, v92), nil)
	started := time.Now()
	sup.CheckNow(ctx)
	waitFor("rolled back", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 2 && events[1] == "update.rolled_back"
	})
	waitFor("0.1.91 back", running("0.1.91"))
	if took := time.Since(started); took > 90*time.Second {
		t.Fatalf("rollback took %v", took)
	}
	if cur, _ := versions.Current(); cur.String() != "0.1.91" {
		t.Fatalf("current = %v", cur)
	}
	if f := versions.Failed(); len(f) != 1 || f[0].String() != "0.1.92" {
		t.Fatalf("failed = %v", f)
	}
}
