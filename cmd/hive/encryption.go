package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/video"
)

// The archive encrypted at rest (ADR-0019): the archive key opens at every
// start, locked when encryption is on; closed segments and stored exports
// are sealed in the background; enabling it shortens the segments.

// sealEvery is how often closed plain segments are looked for and sealed.
const sealEvery = 10 * time.Second

// openArchiveKey opens the archive key in the data directory, after keeping
// the hive's memory, and so the key once unlocked, out of core dumps.
func openArchiveKey(paths hiveconfig.Paths, log *slog.Logger) *archivekey.Vault {
	if err := archivekey.DisableCoreDumps(); err != nil {
		log.Warn("core dumps could not be switched off", "err", err)
	}
	v := archivekey.New(filepath.Join(paths.Data, "archive-key"), archivekey.WithLogger(log))
	if v.State() == archivekey.Locked {
		log.Info("the archive is encrypted and locked until its owner unlocks it")
	}
	return v
}

// segmentMinutes is how long the engine's segments are: at most
// archivekey.SegmentDuration while encryption is on.
func segmentMinutes(configured int, state archivekey.State) int {
	if state == archivekey.Off {
		return configured
	}
	return min(configured, int(archivekey.SegmentDuration/time.Minute))
}

// keyWithEngine is the archive key for the API: switching encryption on
// also shortens the engine's segments, without a restart.
type keyWithEngine struct {
	*archivekey.Vault
	eng video.SegmentDurationSetter // nil without a video engine
	log *slog.Logger
}

// Enable implements hiveapp.ArchiveVault.
func (k keyWithEngine) Enable(passphrase string) error {
	if err := k.Vault.Enable(passphrase); err != nil {
		return err
	}
	if k.eng != nil {
		if err := k.eng.SetSegmentDuration(context.Background(), archivekey.SegmentDuration); err != nil {
			k.log.Error("segment duration for the encrypted archive", "err", err)
		}
	}
	return nil
}

// sealLoop seals closed segments and stored exports while encryption is on,
// at once (what a crash left behind) and then every sealEvery.
func sealLoop(ctx context.Context, vault *archivekey.Vault, idx *archive.Index, root string, exports *export.Manager, log *slog.Logger) {
	t := time.NewTicker(sealEvery)
	defer t.Stop()
	for {
		sealOnce(ctx, vault, idx, root, exports, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sealOnce is one pass of sealLoop; nothing happens while encryption is off.
func sealOnce(ctx context.Context, vault *archivekey.Vault, idx *archive.Index, root string, exports *export.Manager, log *slog.Logger) {
	to := vault.Recipient()
	if to == nil {
		return
	}
	if _, err := idx.Seal(ctx, root, to, time.Now()); err != nil {
		log.Error("sealing segments", "err", err)
	}
	if _, err := exports.Seal(ctx, to); err != nil {
		log.Error("sealing exports", "err", err)
	}
}
