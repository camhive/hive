// Package hiveconfig knows where a hive keeps its files on each OS, reads
// and writes the hive configuration, and owns the hive's identity key
// (ADR-0004): generated locally on first start, never leaving the machine.
package hiveconfig

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/camhive/hive/internal/pki"
)

// Paths are the locations a hive uses. Everything lives under Data except
// the config file on Linux, which follows the /etc convention.
type Paths struct {
	Config  string // hive.yaml
	Data    string // state: key, database, logs, archive by default
	Archive string // recorded video; owners often point this at a big disk
	Logs    string
	Key     string // hive identity private key (PEM, 0600)
	DB      string // SQLite database
	Cert    string // device certificate issued at claim time (PEM)
	CACert  string // cloud Device CA certificate pinned at claim time (PEM)
	Link    string // cloud.json: hive id and cloud URLs, written at claim time
	// Public TLS certificate for <id>.h.camhive.app / <id>.relay.camhive.app
	// (ADR-0012): self-signed until the cloud hands over a public one.
	PublicCert string
	PublicKey  string
}

// DefaultPaths returns the conventional locations for the running OS.
func DefaultPaths() Paths { return DefaultPathsFor(runtimeGOOS, os.Getenv) }

// DefaultPathsFor is DefaultPaths for a given OS and environment (testable).
func DefaultPathsFor(goos string, getenv func(string) string) Paths {
	var data, config string
	switch goos {
	case "windows":
		base := getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		data = base + `\Camhive`
		config = data + `\hive.yaml`
		return Paths{Config: config, Data: data, Archive: data + `\archive`, Logs: data + `\logs`, Key: data + `\hive.key`, DB: data + `\hive.db`,
			Cert: data + `\hive.crt`, CACert: data + `\cloud-ca.crt`, Link: data + `\cloud.json`, PublicCert: data + `\public.crt`, PublicKey: data + `\public.key`}
	case "darwin":
		data = "/Library/Application Support/Camhive"
		config = data + "/hive.yaml"
	default:
		data = "/var/lib/camhive"
		config = "/etc/camhive/hive.yaml"
	}
	return Paths{Config: config, Data: data, Archive: data + "/archive", Logs: data + "/logs", Key: data + "/hive.key", DB: data + "/hive.db",
		Cert: data + "/hive.crt", CACert: data + "/cloud-ca.crt", Link: data + "/cloud.json", PublicCert: data + "/public.crt", PublicKey: data + "/public.key"}
}

// WithDataDir relocates every path under dir (used by --data-dir and tests).
func (p Paths) WithDataDir(dir string) Paths {
	return Paths{
		Config:     filepath.Join(dir, "hive.yaml"),
		Data:       dir,
		Archive:    filepath.Join(dir, "archive"),
		Logs:       filepath.Join(dir, "logs"),
		Key:        filepath.Join(dir, "hive.key"),
		DB:         filepath.Join(dir, "hive.db"),
		Cert:       filepath.Join(dir, "hive.crt"),
		CACert:     filepath.Join(dir, "cloud-ca.crt"),
		Link:       filepath.Join(dir, "cloud.json"),
		PublicCert: filepath.Join(dir, "public.crt"),
		PublicKey:  filepath.Join(dir, "public.key"),
	}
}

// EnsureDirs creates the data, archive and logs directories.
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.Data, p.Archive, p.Logs, filepath.Dir(p.Config)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("hiveconfig: create %s: %w", d, err)
		}
	}
	return nil
}

// Config is the hive's user-editable configuration.
type Config struct {
	ListenAddr string `yaml:"listen_addr"` // local API; loopback only
	ArchiveDir string `yaml:"archive_dir"`
	LogLevel   string `yaml:"log_level"` // debug | info | warn | error
	// CloudPublicKeys verify access tokens: key id -> hex Ed25519 public key.
	// Filled by the cloud at claim time (stage 2); until then tokens from a
	// development issuer can be pasted here.
	CloudPublicKeys map[string]string `yaml:"cloud_public_keys"`
	// SegmentMinutes is the recording segment length (ADR-0009).
	SegmentMinutes int `yaml:"segment_minutes"`
	// RetentionDays keeps footage this many days (0 = until space runs out).
	RetentionDays int `yaml:"retention_days"`
	// MinFreeGB keeps at least this much free on the archive disk; the oldest
	// footage is deleted first. Recording never stops because of a full disk.
	MinFreeGB int `yaml:"min_free_gb"`
	// ExportDays keeps finished exports this many days (0 = until removed by
	// hand); ExportMaxGB then removes the oldest while exports take more
	// (0 = no cap). Old exports go before any footage is deleted for space.
	ExportDays  int `yaml:"export_days"`
	ExportMaxGB int `yaml:"export_max_gb"`
	// ICEServers are STUN/TURN URLs the video engine advertises for WebRTC
	// (ADR-0010). Empty means host candidates only, enough on the LAN.
	// Example: ["stun:stun.camhive.app:3478"].
	ICEServers []string `yaml:"ice_servers"`
	// TLSListenAddr serves the API over TLS with the public certificate on
	// the LAN (ADR-0012); the relay pipes into it over loopback. Empty
	// disables the listener. Access still needs a token.
	TLSListenAddr string `yaml:"tls_listen_addr"`
	// Updates (ADR-0018), read by hive-updater. UpdateURL hosts the signed
	// metadata (an untrusted transport); UpdateChannel is stable or beta;
	// UpdateWindow is when updates may install (local time, HH:MM-HH:MM);
	// Updates is automatic or manual (`hive update`). UpdateRoot names a
	// root.json to trust instead of the one built in (self-built hives).
	UpdateURL     string `yaml:"update_url"`
	UpdateChannel string `yaml:"update_channel"`
	UpdateWindow  string `yaml:"update_window"`
	Updates       string `yaml:"updates"`
	UpdateRoot    string `yaml:"update_root"`
}

func defaults(p Paths) Config {
	return Config{ListenAddr: "127.0.0.1:16016", TLSListenAddr: "0.0.0.0:16443", ArchiveDir: p.Archive, LogLevel: "info", SegmentMinutes: 10, RetentionDays: 30, MinFreeGB: 10, ExportDays: 7, ExportMaxGB: 10,
		UpdateURL: "https://updates.dev.camhive.app", UpdateChannel: "stable", UpdateWindow: "02:00-05:00", Updates: "automatic"}
}

// Validate rejects configurations we refuse to run with.
func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("hiveconfig: listen_addr: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("hiveconfig: listen_addr must be a loopback address; remote access goes through access tokens and the tunnel, never an open port")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("hiveconfig: log_level %q is not one of debug, info, warn, error", c.LogLevel)
	}
	if c.ArchiveDir == "" {
		return errors.New("hiveconfig: archive_dir is required")
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	return c.validateUpdates()
}

var windowRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$`)

// validateUpdates checks the update settings (ADR-0018).
func (c Config) validateUpdates() error {
	if u, err := url.Parse(c.UpdateURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("hiveconfig: update_url %q must be an https URL", c.UpdateURL)
	}
	if c.UpdateChannel != "stable" && c.UpdateChannel != "beta" {
		return fmt.Errorf("hiveconfig: update_channel %q is not stable or beta", c.UpdateChannel)
	}
	if !windowRE.MatchString(c.UpdateWindow) || c.UpdateWindow[:5] == c.UpdateWindow[6:] {
		return fmt.Errorf("hiveconfig: update_window %q must look like 02:00-05:00", c.UpdateWindow)
	}
	if c.Updates != "automatic" && c.Updates != "manual" {
		return fmt.Errorf("hiveconfig: updates %q is not automatic or manual", c.Updates)
	}
	return nil
}

// validateStorage checks the recording and keeping limits.
func (c Config) validateStorage() error {
	if c.SegmentMinutes < 1 || c.SegmentMinutes > 60 {
		return errors.New("hiveconfig: segment_minutes must be between 1 and 60")
	}
	if c.RetentionDays < 0 || c.MinFreeGB < 0 {
		return errors.New("hiveconfig: retention_days and min_free_gb must not be negative")
	}
	if c.ExportDays < 0 || c.ExportMaxGB < 0 {
		return errors.New("hiveconfig: export_days and export_max_gb must not be negative")
	}
	return nil
}

// Load reads the config file, or writes the defaults and returns them when
// no file exists yet (firstRun = true). Missing keys take default values.
func Load(p Paths) (cfg Config, firstRun bool, err error) {
	cfg = defaults(p)
	raw, err := os.ReadFile(p.Config) //nolint:gosec // path from our own Paths
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := p.EnsureDirs(); err != nil {
			return Config{}, false, err
		}
		out, _ := yaml.Marshal(cfg)
		header := []byte("# Camhive hive configuration. Edit and restart the service.\n")
		if err := os.WriteFile(p.Config, append(header, out...), 0o600); err != nil {
			return Config{}, false, fmt.Errorf("hiveconfig: write default config: %w", err)
		}
		return cfg, true, nil
	case err != nil:
		return Config{}, false, fmt.Errorf("hiveconfig: read %s: %w", p.Config, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("hiveconfig: parse %s: %w", p.Config, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, false, nil
}

// LoadOrCreateKey returns the hive's Ed25519 identity key, generating and
// persisting it (PEM, 0600) on first use.
func LoadOrCreateKey(p Paths) (key ed25519.PrivateKey, created bool, err error) {
	raw, err := os.ReadFile(p.Key) //nolint:gosec // path from our own Paths
	switch {
	case err == nil:
		key, err = pki.ParsePrivateKey(raw)
		if err != nil {
			return nil, false, fmt.Errorf("hiveconfig: %s: %w", p.Key, err)
		}
		return key, false, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, false, fmt.Errorf("hiveconfig: read key: %w", err)
	}
	if err := p.EnsureDirs(); err != nil {
		return nil, false, err
	}
	_, key, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(p.Key, pki.MarshalPrivateKey(key), 0o600); err != nil {
		return nil, false, fmt.Errorf("hiveconfig: write key: %w", err)
	}
	return key, true, nil
}

// StoreKey derives the 32-byte key that encrypts secrets in the local
// database from the hive identity key (HKDF-SHA256, fixed info string).
func StoreKey(key ed25519.PrivateKey) ([]byte, error) {
	return hkdf.Key(sha256.New, key.Seed(), nil, "camhive hive store v1", 32)
}

// LocalHiveID derives a stable id from the public key, used before the cloud
// assigns one at claim time: "hv_" + first 16 hex of SHA-256(pub).
func LocalHiveID(pub ed25519.PublicKey) pki.HiveID {
	sum := sha256.Sum256(pub)
	return pki.HiveID("hv_" + hex.EncodeToString(sum[:8]))
}
