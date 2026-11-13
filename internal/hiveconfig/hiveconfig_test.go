package hiveconfig_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
)

func TestDefaultPathsPerOS(t *testing.T) {
	cases := []struct {
		goos      string
		env       map[string]string
		wantData  string
		wantConf  string
		wantArchv string
	}{
		{"linux", nil, "/var/lib/camhive", "/etc/camhive/hive.yaml", "/var/lib/camhive/archive"},
		{"windows", map[string]string{"ProgramData": `C:\ProgramData`}, `C:\ProgramData\Camhive`, `C:\ProgramData\Camhive\hive.yaml`, `C:\ProgramData\Camhive\archive`},
		{"darwin", nil, "/Library/Application Support/Camhive", "/Library/Application Support/Camhive/hive.yaml", "/Library/Application Support/Camhive/archive"},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			p := hiveconfig.DefaultPathsFor(tc.goos, func(k string) string { return tc.env[k] })
			if p.Data != tc.wantData || p.Config != tc.wantConf || p.Archive != tc.wantArchv {
				t.Fatalf("paths = %+v", p)
			}
			if p.Logs == "" || p.Key == "" {
				t.Fatalf("logs/key paths must be set: %+v", p)
			}
		})
	}
}

func TestDataDirOverrideMovesEverything(t *testing.T) {
	// WithDataDir uses the running OS's separators (it is what --data-dir does).
	dir := filepath.Join(t.TempDir(), "srv", "hive")
	p := hiveconfig.DefaultPathsFor("linux", func(string) string { return "" }).WithDataDir(dir)
	if p.Data != dir || p.Config != filepath.Join(dir, "hive.yaml") || p.Archive != filepath.Join(dir, "archive") || !strings.HasPrefix(p.Key, dir+string(filepath.Separator)) {
		t.Fatalf("paths = %+v", p)
	}
}

func TestLoadWritesDefaultsOnFirstRunAndReadsThemBack(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(dir)

	cfg, firstRun, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !firstRun {
		t.Fatal("expected firstRun on an empty data dir")
	}
	if cfg.ListenAddr != "127.0.0.1:16016" || cfg.ArchiveDir != paths.Archive || cfg.LogLevel != "info" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := os.Stat(paths.Config); err != nil {
		t.Fatalf("config file must be written on first run: %v", err)
	}

	// Edit and reload.
	raw, _ := os.ReadFile(paths.Config)
	edited := strings.Replace(string(raw), "127.0.0.1:16016", "127.0.0.1:17000", 1)
	if err := os.WriteFile(paths.Config, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2, firstRun2, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if firstRun2 || cfg2.ListenAddr != "127.0.0.1:17000" {
		t.Fatalf("reload = %+v firstRun=%v", cfg2, firstRun2)
	}
}

func TestLoadRejectsBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(dir)
	_ = os.MkdirAll(dir, 0o750)
	_ = os.WriteFile(paths.Config, []byte("listen_addr: [not a string"), 0o600)
	if _, _, err := hiveconfig.Load(paths); err == nil {
		t.Fatal("broken YAML must be an error, not silently defaulted")
	}
	_ = os.WriteFile(paths.Config, []byte("listen_addr: 0.0.0.0:16016\n"), 0o600)
	if _, _, err := hiveconfig.Load(paths); err == nil {
		t.Fatal("binding all interfaces must be rejected (remote access goes through tokens and the tunnel)")
	}
}

func TestKeyIsCreatedOnceAndReused(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(dir)

	k1, created, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first call must create the key")
	}
	k2, created2, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if created2 || !k1.Public().(ed25519.PublicKey).Equal(k2.Public().(ed25519.PublicKey)) {
		t.Fatal("second call must reuse the same key")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(paths.Key)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode = %o, want 0600", st.Mode().Perm())
		}
	}
	id := hiveconfig.LocalHiveID(k1.Public().(ed25519.PublicKey))
	if !strings.HasPrefix(string(id), "hv_") || len(id) != 3+16 || !id.Valid() {
		t.Fatalf("local hive id = %q", id)
	}
	if hiveconfig.LocalHiveID(k2.Public().(ed25519.PublicKey)) != id {
		t.Fatal("id must be deterministic for the key")
	}
}

func TestStoreKeyIsDeterministicAndDistinctPerHive(t *testing.T) {
	_, k1, _ := ed25519.GenerateKey(nil)
	_, k2, _ := ed25519.GenerateKey(nil)
	a, err := hiveconfig.StoreKey(k1)
	if err != nil || len(a) != 32 {
		t.Fatalf("key = %x err=%v", a, err)
	}
	b, _ := hiveconfig.StoreKey(k1)
	c, _ := hiveconfig.StoreKey(k2)
	if string(a) != string(b) || string(a) == string(c) {
		t.Fatal("store key must be deterministic per hive key and differ between hives")
	}
}

func TestEnsureDirsCreatesTree(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(filepath.Join(dir, "nested", "hive"))
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{paths.Data, paths.Archive, paths.Logs} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("%s not created: %v", d, err)
		}
	}
}

// #59: exports are kept 7 days and up to 10 GB by default; limits are not negative.
func TestExportLimitsDefaultAndValidation(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExportDays != 7 || cfg.ExportMaxGB != 10 {
		t.Fatalf("export defaults = %d days, %d GB", cfg.ExportDays, cfg.ExportMaxGB)
	}
	for _, bad := range []hiveconfig.Config{{ExportDays: -1}, {ExportMaxGB: -1}} {
		c := cfg
		c.ExportDays, c.ExportMaxGB = bad.ExportDays, bad.ExportMaxGB
		if err := c.Validate(); err == nil {
			t.Errorf("negative export limit accepted: %+v", bad)
		}
	}
	c := cfg
	c.ExportDays, c.ExportMaxGB = 0, 0 // keep until removed by hand
	if err := c.Validate(); err != nil {
		t.Fatalf("zero disables a limit: %v", err)
	}
}

// Each limit is checked; the error names the setting.
func TestValidateNamesTheBadSetting(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	good, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*hiveconfig.Config){
		"listen_addr":     func(c *hiveconfig.Config) { c.ListenAddr = "no port" },
		"log_level":       func(c *hiveconfig.Config) { c.LogLevel = "loud" },
		"archive_dir":     func(c *hiveconfig.Config) { c.ArchiveDir = "" },
		"segment_minutes": func(c *hiveconfig.Config) { c.SegmentMinutes = 61 },
		"retention_days":  func(c *hiveconfig.Config) { c.RetentionDays = -1 },
		"min_free_gb":     func(c *hiveconfig.Config) { c.MinFreeGB = -1 },
		"export_max_gb":   func(c *hiveconfig.Config) { c.ExportMaxGB = -1 },
		"update_url":      func(c *hiveconfig.Config) { c.UpdateURL = "ftp://x" },
		"update_channel":  func(c *hiveconfig.Config) { c.UpdateChannel = "nightly" },
		"update_window":   func(c *hiveconfig.Config) { c.UpdateWindow = "2-5" },
		"updates":         func(c *hiveconfig.Config) { c.Updates = "sometimes" },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c := good
	c.SegmentMinutes = 0
	if err := c.Validate(); err == nil {
		t.Error("segment_minutes 0 accepted")
	}
	// The edges are allowed: 1 and 60 minutes, zero limits (off).
	for _, edge := range []func(*hiveconfig.Config){
		func(c *hiveconfig.Config) { c.SegmentMinutes = 1 },
		func(c *hiveconfig.Config) { c.SegmentMinutes = 60 },
		func(c *hiveconfig.Config) { c.RetentionDays, c.MinFreeGB, c.ExportDays, c.ExportMaxGB = 0, 0, 0, 0 },
	} {
		c := good
		edge(&c)
		if err := c.Validate(); err != nil {
			t.Errorf("edge rejected: %v", err)
		}
	}
}

// ADR-0018: updates are on by default, nightly, from the stable channel of
// the dev metadata host until the production stack exists.
func TestUpdateSettingsDefaults(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	c, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if c.UpdateURL != "https://updates.dev.camhive.app" || c.UpdateChannel != "stable" || c.UpdateWindow != "02:00-05:00" || c.Updates != "automatic" || c.UpdateRoot != "" {
		t.Fatalf("defaults = %q %q %q %q %q", c.UpdateURL, c.UpdateChannel, c.UpdateWindow, c.Updates, c.UpdateRoot)
	}
	c.UpdateWindow = "23:30-01:00"
	c.Updates = "manual"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
