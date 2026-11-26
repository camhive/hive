package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/protocol"
)

// #172: under an assembled hive, the archive root vanishing reaches the
// agent's alarm lane as recording.stopped at the next check, without the
// store, and recording.resumed when it is back.
func TestAHiveWhoseArchiveVanishesTellsTheCloudAtTheNextCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths := hiveconfig.DefaultPaths().WithDataDir(t.TempDir())
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.ArchiveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	parts, err := assemble(ctx, cfg, paths, key, "hv_test", "0.1.11", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer parts.close()
	sent := make(chan protocol.Event, 8)
	go parts.agent.RunAlarms(ctx, func(_ context.Context, e protocol.Event) error { sent <- e; return nil }, 10*time.Millisecond)
	next := func() protocol.Event {
		t.Helper()
		select {
		case e := <-sent:
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("no alarm reached the cloud link")
		}
		return protocol.Event{}
	}

	parts.recording.Check(ctx) // the start: recording
	// #172 review: the start's judgement goes once on the plain queue, never
	// on the alarm lane, so an old gateway that never acks sees no loop.
	if parts.agent.Pending() != 1 || parts.agent.PendingAlarms() != 0 {
		t.Fatalf("after the start: %d queued, %d alarms; want the start's recording.resumed queued once, no alarm", parts.agent.Pending(), parts.agent.PendingAlarms())
	}
	if err := os.RemoveAll(cfg.ArchiveDir); err != nil {
		t.Fatal(err)
	}
	parts.recording.Check(ctx) // one check interval later
	e := next()
	var data struct{ Reason string }
	if e.Name != "recording.stopped" || json.Unmarshal(e.Data, &data) != nil || data.Reason != "archive_missing" {
		t.Fatalf("alarm = %s %s, want recording.stopped with reason archive_missing", e.Name, e.Data)
	}
	if err := os.MkdirAll(cfg.ArchiveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	parts.recording.Check(ctx)
	e = next()
	var end struct {
		StoppedAt time.Time `json:"stopped_at"`
	}
	if e.Name != "recording.resumed" || json.Unmarshal(e.Data, &end) != nil || end.StoppedAt.IsZero() {
		t.Fatalf("alarm = %s %s, want recording.resumed naming the episode it ends", e.Name, e.Data)
	}
}
