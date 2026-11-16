package main

import (
	"log/slog"
	"path/filepath"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
)

// policies reads the owner's update settings from hive.yaml at every
// check (#82): the app writes them there, and the next check follows them
// without a restart. Only Run's goroutine calls current.
type policies struct {
	paths   hiveconfig.Paths
	dir     string // the updater's private directory
	release bool   // a dev build never updates
	log     *slog.Logger
	last    update.Policy // the last good one, kept when hive.yaml breaks
}

func newPolicies(paths hiveconfig.Paths, dir string, release bool, log *slog.Logger) *policies {
	return &policies{paths: paths, dir: dir, release: release, log: log}
}

// current is the policy hive.yaml says now, or the last good one.
func (p *policies) current() update.Policy {
	cfg, err := hiveconfig.Read(p.paths)
	if err == nil {
		var pol update.Policy
		if pol, err = p.from(cfg); err == nil {
			p.last = pol
			return pol
		}
	}
	p.log.Warn("update settings unreadable; keeping the last good ones", "err", err)
	return p.last
}

func (p *policies) from(cfg hiveconfig.Config) (update.Policy, error) {
	window, err := update.ParseWindow(cfg.UpdateWindow)
	if err != nil {
		return update.Policy{}, err
	}
	pol := update.Policy{Mode: update.Mode(cfg.Updates), Window: window}
	root, err := trustedRoot(cfg)
	if err != nil || !p.release || root == nil {
		return pol, err // no source: no updates
	}
	pol.Source = &update.SourceChannel{Platform: update.Platform(), Source: update.Source{
		MetadataURL: cfg.UpdateURL + "/" + cfg.UpdateChannel, Root: root, CacheDir: cacheDir(p.dir, cfg.UpdateChannel)}}
	return pol, nil
}

// cacheDir keeps each channel's trusted metadata apart: their versions
// differ, and TUF refuses metadata older than what it has seen. stable
// keeps the directory it always had.
func cacheDir(dir, channel string) string {
	if channel == "stable" {
		return filepath.Join(dir, "tuf")
	}
	return filepath.Join(dir, "tuf-"+channel)
}
