package diag_test

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/diag"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
)

func TestBundleContainsEverythingAndNoSecrets(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor("linux", func(string) string { return "" }).WithDataDir(dir)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(paths.Config, []byte("listen_addr: 127.0.0.1:16016\narchive_dir: "+paths.Archive+"\nlog_level: info\nsegment_minutes: 10\nretention_days: 30\nmin_free_gb: 10\nsmtp_password: hunter2\n"), 0o600)
	_ = os.WriteFile(filepath.Join(paths.Logs, "hive.log"), []byte(`{"msg":"started"}`+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(paths.Logs, "hive-2026-11-08.log"), []byte(`{"msg":"old"}`+"\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(paths.Data, "engine"), 0o750)
	_ = os.WriteFile(filepath.Join(paths.Data, "engine", "mediamtx.log"), []byte("mtx\n"), 0o600)
	_ = os.WriteFile(paths.Key, []byte("-----BEGIN PRIVATE KEY-----\nSECRETKEY\n-----END PRIVATE KEY-----\n"), 0o600)

	st, err := store.Open(context.Background(), paths.DB, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://admin:topsecret@10.0.0.5/live"}}, time.Now())
	_ = st.Cameras().Put(context.Background(), cam)
	_ = st.Actions().Append(context.Background(), store.Action{Actor: "acc", Action: "camera.add", Target: string(cam.ID)})
	_ = st.Segments().LogDeletion(context.Background(), store.Deletion{DeletedAt: time.Now(), Camera: string(cam.ID), Stream: "main", Start: time.Now(), Bytes: 5, Reason: "age"})

	out, entries, err := diag.Build(context.Background(), diag.Inputs{
		Paths: paths, Store: st, Version: "0.1.0-test", EngineVersion: func(context.Context) string { return "mediamtx v1.21.3" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != paths.Data || !strings.HasPrefix(filepath.Base(out), "camhive-diag-") || !strings.HasSuffix(out, ".zip") {
		t.Fatalf("bundle path = %s", out)
	}
	want := []string{"README.txt", "versions.json", "config.yaml", "cameras.json", "actions.json", "deletions.json", "disk.json", "logs/hive.log", "logs/hive-2026-11-08.log", "engine/mediamtx.log"}
	for _, w := range want {
		if !contains(entries, w) {
			t.Errorf("bundle missing %s (have %v)", w, entries)
		}
	}
	if contains(entries, "hive.key") || contains(entries, "hive.db") {
		t.Fatalf("bundle must not include the key or the database: %v", entries)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	all := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		all[f.Name] = string(b)
	}
	for name, body := range all {
		for _, secret := range []string{"topsecret", "hunter2", "SECRETKEY"} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s leaks %q", name, secret)
			}
		}
	}
	if !strings.Contains(all["config.yaml"], "smtp_password: ***") {
		t.Fatalf("password-like config keys must be redacted:\n%s", all["config.yaml"])
	}
	if !strings.Contains(all["cameras.json"], "admin") || !strings.Contains(all["cameras.json"], "Door") {
		t.Fatalf("cameras.json should list cameras with usernames:\n%s", all["cameras.json"])
	}
	if !strings.Contains(all["versions.json"], "0.1.0-test") || !strings.Contains(all["versions.json"], "mediamtx v1.21.3") {
		t.Fatalf("versions.json:\n%s", all["versions.json"])
	}
	if !strings.Contains(all["actions.json"], "camera.add") || !strings.Contains(all["deletions.json"], `"age"`) {
		t.Fatalf("logs from the store missing")
	}
}

func TestBundleWithoutStoreOrLogsStillBuilds(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor("linux", func(string) string { return "" }).WithDataDir(dir)
	out, entries, err := diag.Build(context.Background(), diag.Inputs{Paths: paths, Version: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil || !contains(entries, "README.txt") || !contains(entries, "versions.json") {
		t.Fatalf("minimal bundle: %v %v", err, entries)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
