package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
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

// updaterNextTo is hive-updater beside this executable, or "".
func updaterNextTo() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	name := "hive-updater"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// requestUpdate asks the running hive-updater to install what is offered
// now (`hive update`), whatever the window or manual mode says. Only who
// can write the updater's private directory can ask.
func requestUpdate(dir string, stdout, stderr io.Writer) int {
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintln(stderr, "hive-updater does not run on this machine (no", dir+")")
		return 1
	}
	if err := os.WriteFile(filepath.Join(dir, "update-request"), nil, 0o600); err != nil {
		fmt.Fprintln(stderr, "cannot ask for an update:", err, "(run as root / Administrator)")
		return 1
	}
	fmt.Fprintln(stdout, "update requested: hive-updater checks within a minute; see `hive status` and the activity log")
	return 0
}
