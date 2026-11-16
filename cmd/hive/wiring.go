package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/diag"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/events"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/ingest"
	"github.com/camhive/hive/internal/locate"
	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/ptz"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/update/journal"
	"github.com/camhive/hive/internal/video"
)

// runtimeParts are the long-lived components the service starts and stops.
type runtimeParts struct {
	handler http.Handler
	agent   *hiveagent.Agent // cloud command handler and event source
	ingest  *ingest.Ingest   // nil when the video engine is unavailable
	index   *archive.Index   // nil when the video engine is unavailable
	sweeper *retention.Sweeper
	health  *health.Monitor
	locator *locate.Locator // nil when the video engine is unavailable
	motion  *motion.Hub     // turns frame sizes and camera events into motion Events
	cameras *motion.Watcher // nil when the video engine is unavailable; the cameras' own motion events
	store   *store.Store
	archive string
	vault   *archivekey.Vault // the archive key (ADR-0019)
	exports *export.Manager   // nil when the video engine is unavailable
	close   func()
}

// assemble opens the store, builds the verifier, the video engine, the
// ingest loop and the HTTP API from config and the hive key.
func assemble(ctx context.Context, cfg hiveconfig.Config, paths hiveconfig.Paths, key ed25519.PrivateKey, hiveID string, version string, log *slog.Logger) (*runtimeParts, error) {
	storeKey, err := hiveconfig.StoreKey(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, paths.DB, storeKey)
	if err != nil {
		return nil, err
	}

	keys, err := cloudKeys(cfg, log)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if pushed, ok := hiveagent.LoadKeys(ctx, st); ok {
		keys = pushed // keys the cloud pushed over the channel win over hive.yaml
	}
	licensing := hiveagent.NewLicensing(nil)
	verifier := auth.NewVerifier(hiveID, keys, auth.WithLicenseValid(licensing.Usable))

	vault := openArchiveKey(paths, log.With("component", "archivekey"))
	parts := &runtimeParts{close: func() { vault.Lock(); _ = st.Close() }, store: st, vault: vault} // Lock wipes the key
	closeOpenEvents(ctx, st, log)
	parts.motion = motion.NewHub(st.Events(), motion.Options{Logger: log.With("component", "motion"),
		Observe: func(e store.Event) { parts.agent.PromptStart(e) }}) // motion starts reach the cloud only while fresh (ADR-0021)
	cfg.SegmentMinutes = segmentMinutes(cfg.SegmentMinutes, vault.State())
	eng, err := newEngine(cfg, paths, log, video.WithFrames(parts.motion.Frame))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	parts.agent = hiveagent.New(hiveagent.Deps{Store: st, Verifier: verifier, License: licensing, Version: version, Logger: log.With("component", "agent"),
		Free: func() (int64, error) { return retention.FreeSpace(cfg.ArchiveDir) },
		Diag: func(ctx context.Context) (string, []string, error) {
			return diag.Build(ctx, diag.Inputs{Paths: paths, Store: st, Version: version, Free: retention.FreeSpace})
		}})
	parts.agent.LoadLicense(ctx)
	deps := hiveapp.Deps{Store: st, Verifier: verifier, Version: version, Notify: func() {}, Logger: log.With("component", "api"), Location: time.Local, PTZ: ptz.New(),
		Licensed: licensing.Effective, Discovery: hiveapp.ONVIFDiscoverer{},
		Local:   auth.NewLocalSessions(hiveID, time.Now),    // the owner's emergency login (#57), LAN listener only
		Updates: updateControl(paths, journal.DefaultDir()), // update settings and "Update now" (#82)
		Vault:   keyWithEngine{Vault: vault, log: log}, Emit: parts.agent.Emit}
	if eng != nil {
		deps.Vault = keyWithEngine{Vault: vault, eng: eng, log: log}
		parts.ingest = ingest.New(st.Cameras(), eng, ingest.Options{Logger: log.With("component", "ingest")})
		parts.index = archive.NewIndex(st, archive.WithLogger(log.With("component", "archive")), archive.WithOpener(vault.OpenSegment))
		parts.archive = cfg.ArchiveDir
		deps.Playback = hiveapp.ArchiveFetch(archive.NewReader(cfg.ArchiveDir, vault.OpenSegment))
		deps.Exports = export.NewManager(filepath.Join(paths.Data, "exports"), deps.Playback, st.Exports(), export.WithVault(vault),
			export.WithKeep(export.Keep{MaxAge: time.Duration(cfg.ExportDays) * 24 * time.Hour, MaxBytes: int64(cfg.ExportMaxGB) << 30}),
			export.WithOnDone(func(j export.Job) {
				details, _ := json.Marshal(map[string]any{"camera": j.Request.Camera, "stream": j.Request.Stream, "start": j.Request.Start, "duration_s": int(j.Request.Duration.Seconds()), "status": j.Status, "bytes": j.Bytes, "error": j.Error})
				if err := st.Actions().Append(context.Background(), store.Action{Actor: j.Request.Owner, Action: "export." + string(j.Status), Target: j.ID, Details: details}); err != nil {
					log.Error("action log", "err", err)
				}
			}))
		if err := deps.Exports.Recover(ctx); err != nil { // jobs cut off by the last stop
			log.Error("export recovery", "err", err)
		}
		parts.exports = deps.Exports
		storageLow := events.NewCondition(st.Events(), "", hiveagent.EventStorageLow, events.WithLogger(log), events.Observe(parts.agent.EmitStart))
		parts.sweeper = retention.New(parts.index, st.Segments(),
			retention.Policy{DefaultDays: cfg.RetentionDays, CameraDays: st.Cameras().RetentionDays, MinFreeBytes: int64(cfg.MinFreeGB) << 30,
				MaxDays: func() int { return licensing.Effective().Int(entitlements.RetentionDaysMax) }},
			retention.Options{ArchiveDir: cfg.ArchiveDir, Logger: log.With("component", "retention"), Exports: deps.Exports,
				OnStorageLow: func(free int64) {
					log.Error("storage low: archive disk below min_free_gb and nothing left to delete", "free_bytes", free)
					storageLow.Begin(context.Background(), time.Now(), map[string]any{"free_bytes": free, "min_free_gb": cfg.MinFreeGB})
				},
				OnStorageOK: func(int64) { storageLow.End(context.Background(), time.Now()) },
			})
		deps.Deletions = parts.sweeper
		parts.cameras = motion.NewWatcher(st.Cameras(), parts.motion, motion.WatchOptions{Logger: log.With("component", "motion")})
		deps.Notify = func() { parts.ingest.Notify(); parts.cameras.Notify() }
		deps.Index = parts.index
		deps.Engine = eng
		parts.health = health.New(st.Cameras(), eng, health.Options{
			Logger: log.With("component", "health"),
			OnEvent: func(e health.Event) {
				log.Info("camera event", "kind", e.Kind, "camera", e.Camera)
				ev := store.Event{Camera: e.Camera, Kind: e.Kind, At: e.At, Data: e.Data}
				id, err := st.Events().Append(context.Background(), ev)
				if err != nil {
					log.Error("store event", "err", err)
				}
				ev.ID = id // 0 when the write failed: the cloud then keys on camera and time
				parts.agent.EmitEvent(ev)
			},
		})
		deps.Health = parts.health
		parts.locator = newLocator(st, parts, log)
		deps.Locator = parts.locator
		parts.agent.SetHealth(parts.health)
		parts.agent.SetEngine(eng)
	}
	parts.handler = hiveapp.NewAPI(deps)
	return parts, nil
}

// closeOpenEvents ends the lasting Events a crash left open (ADR-0020): the
// conditions are measured afresh and begin again if they still hold.
func closeOpenEvents(ctx context.Context, st *store.Store, log *slog.Logger) {
	if n, err := st.Events().CloseOpen(ctx); err != nil {
		log.Error("close open events", "err", err)
	} else if n > 0 {
		log.Info("closed events left open by the last run", "count", n)
	}
}

// cloudKeys parses the configured verification keys; none is allowed (the
// hive is not claimed yet) but means every API request is rejected.
func cloudKeys(cfg hiveconfig.Config, log *slog.Logger) (auth.Keys, error) {
	if len(cfg.CloudPublicKeys) == 0 {
		log.Warn("no cloud_public_keys configured; every API request will be rejected until the hive is claimed")
		return nil, nil
	}
	raw, err := json.Marshal(cfg.CloudPublicKeys)
	if err != nil {
		return nil, err
	}
	var keys auth.Keys
	if err := keys.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("cloud_public_keys: %w", err)
	}
	return keys, nil
}

// newEngine builds the MediaMTX engine, or returns nil (and logs) when the
// binary is missing so the API still runs.
func newEngine(cfg hiveconfig.Config, paths hiveconfig.Paths, log *slog.Logger, opts ...video.Option) (*video.MediaMTX, error) {
	c := engineConfig(cfg, paths)
	c.Binary = mediamtxBinary()
	eng, err := video.NewMediaMTX(c, append([]video.Option{video.WithLogger(log.With("component", "video"))}, opts...)...)
	if errors.Is(err, video.ErrMissingTool) {
		log.Error("video engine unavailable: mediamtx binary not found; cameras will not record", "err", err)
		return nil, nil
	}
	return eng, err
}

// engineConfig is what the engine takes from hive.yaml, its ports (#61)
// included.
func engineConfig(cfg hiveconfig.Config, paths hiveconfig.Paths) video.MediaMTXConfig {
	ice := make([]video.ICEServer, 0, len(cfg.ICEServers))
	for _, u := range cfg.ICEServers {
		ice = append(ice, video.ICEServer{URL: u})
	}
	return video.MediaMTXConfig{
		ArchiveDir: cfg.ArchiveDir, SegmentDuration: time.Duration(cfg.SegmentMinutes) * time.Minute,
		WorkDir: filepath.Join(paths.Data, "engine"), ICEServers: ice,
		APIPort: cfg.EngineAPIPort, WebRTCPort: cfg.EngineWebRTCPort, WebRTCUDPPort: cfg.EngineWebRTCUDPPort,
	}
}

// mediamtxBinary prefers the mediamtx shipped next to the hive executable
// (release layout) and falls back to PATH (development). Empty lets the
// engine search PATH itself.
func mediamtxBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return mediamtxNextTo(exe)
}

// mediamtxNextTo returns the engine beside the real hive executable: archives
// unpack both into one folder, deb and rpm install both in /usr/lib/camhive
// and link /usr/bin/hive there (#62). Empty when there is none.
func mediamtxNextTo(exe string) string {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	name := "mediamtx"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(filepath.Dir(real), name)
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

// newLocator finds cameras again after an address change (ADR-0017) and
// reports what it did as Events, in the action log and to the cloud.
func newLocator(st *store.Store, parts *runtimeParts, log *slog.Logger) *locate.Locator {
	report := func(kind string, cam camera.ID, data map[string]any) {
		raw, _ := json.Marshal(data)
		if _, err := st.Events().Append(context.Background(), store.Event{Camera: string(cam), Kind: kind, At: time.Now().UTC(), Data: raw}); err != nil {
			log.Error("store event", "err", err)
		}
		if err := st.Actions().Append(context.Background(), store.Action{Actor: "hive", Action: kind, Target: string(cam), Details: raw}); err != nil {
			log.Error("action log", "err", err)
		}
		parts.agent.Emit(kind, map[string]any{"camera": cam, "data": data})
	}
	health := func(id camera.ID) (bool, time.Time) {
		h := parts.health.Camera(string(id))
		return h.Online, h.Streams[string(camera.Main)].Since
	}
	return locate.New(st.Cameras(), locate.System{}, locate.Credentials{}, health, locate.Options{
		Logger: log.With("component", "locate"),
		OnMoved: func(old, moved camera.Camera) {
			report("camera.address_changed", moved.ID, map[string]any{"old": old.Host(), "new": moved.Host()})
			parts.ingest.Notify() // pull from the new address
			parts.cameras.Notify()
		},
		OnConflict: func(c camera.Camera, fields []string) {
			report("camera.identity_changed", c.ID, map[string]any{"host": c.Host(), "fields": fields})
		},
	})
}
