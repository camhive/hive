package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/update/journal"
)

// reportUpdates hands what hive-updater did (ADR-0018 §7) to the action log
// and the cloud, now and every minute: the updater writes "applied" only
// after this hive has started and passed its health check.
func reportUpdates(ctx context.Context, dir string, st *store.Store, agent *hiveagent.Agent, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		entries, err := journal.Drain(dir)
		if err != nil {
			log.Warn("update journal", "err", err)
		}
		for _, e := range entries {
			raw, _ := json.Marshal(e.Data)
			if err := st.Actions().Append(ctx, store.Action{Actor: "updater", Action: e.Kind, Target: "hive", Details: raw}); err != nil {
				log.Error("action log", "err", err)
			}
			agent.Emit(e.Kind, map[string]any{"at": e.At, "data": e.Data})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
