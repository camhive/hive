package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/logging"
)

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"rtsp://admin:topsecret@10.0.0.5:554/live": "rtsp://admin:***@10.0.0.5:554/live",
		"rtsp://admin@10.0.0.5/live":               "rtsp://admin@10.0.0.5/live",
		"rtsp://10.0.0.5/live":                     "rtsp://10.0.0.5/live",
		"http://u:p%40ss@cam/onvif/device_service": "http://u:***@cam/onvif/device_service",
		"not a url with p@ss":                      "not a url with p@ss",
		"":                                         "",
	}
	for in, want := range cases {
		if got := logging.RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Logging domain values through slog must never reveal a password, whatever
// form the value takes: the struct, a pointer, a URL with credentials.
func TestSlogNeverLeaksCredentials(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://admin:topsecret@h/live"}}, time.Now())
	log.Info("camera", "cam", cam, "creds", cam.Credentials, "credsPtr", &cam.Credentials, "url", logging.RedactURL("rtsp://admin:topsecret@h/live"))
	log.Info("source", "src", cam.Source, "spec", camera.Spec{Credentials: camera.Credentials{Password: "topsecret"}})
	out := buf.String()
	if strings.Contains(out, "topsecret") {
		t.Fatalf("password leaked into log:\n%s", out)
	}
	if !strings.Contains(out, "admin") {
		t.Fatalf("username should remain visible:\n%s", out)
	}
}

func TestNewWritesJSONToFileAndTextToConsole(t *testing.T) {
	dir := t.TempDir()
	var console bytes.Buffer
	log, closeFn, err := logging.New(logging.Config{Level: "debug", File: filepath.Join(dir, "hive.log"), Console: &console, ConsoleJSON: false})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hello", "k", "v")
	log.Debug("dbg")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "hive.log"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("file lines = %d\n%s", len(lines), raw)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil || rec["msg"] != "hello" || rec["k"] != "v" {
		t.Fatalf("file must be JSON lines: %v %s", err, lines[0])
	}
	if !strings.Contains(console.String(), "msg=hello") || !strings.Contains(console.String(), "k=v") {
		t.Fatalf("console must be text: %s", console.String())
	}
}

func TestLevelFiltersAndInvalidLevelIsAnError(t *testing.T) {
	var console bytes.Buffer
	log, closeFn, err := logging.New(logging.Config{Level: "warn", Console: &console})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	log.Info("quiet")
	log.Warn("loud")
	if strings.Contains(console.String(), "quiet") || !strings.Contains(console.String(), "loud") {
		t.Fatalf("level filter: %s", console.String())
	}
	if _, _, err := logging.New(logging.Config{Level: "chatty"}); err == nil {
		t.Fatal("unknown level must be rejected")
	}
}

func TestFileRotatesAtMaxSize(t *testing.T) {
	dir := t.TempDir()
	log, closeFn, err := logging.New(logging.Config{Level: "info", File: filepath.Join(dir, "hive.log"), MaxSizeMB: 1, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 1000)
	for range 1500 { // ~1.5 MB
		log.Info("fill", "p", payload)
	}
	_ = closeFn()
	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Fatalf("expected a rotated file, got %v", names(entries))
	}
	for _, e := range entries {
		if st, _ := e.Info(); st.Size() > 1200*1024 {
			t.Fatalf("%s is %d bytes, larger than the 1 MB limit", e.Name(), st.Size())
		}
	}
}

func names(es []os.DirEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name()
	}
	return out
}
