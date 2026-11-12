package journal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/camhive/hive/internal/update/journal"
)

// ADR-0018 §7: the updater writes what it did; the hive sends each entry
// once, also when it stopped halfway through sending.
func TestEntriesAreHandedOverOnceAndSurviveAnInterruptedDrain(t *testing.T) {
	dir := t.TempDir()
	if got, err := journal.Drain(dir); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	if err := journal.Append(dir, "update.applied", map[string]any{"from": "0.1.4", "to": "0.1.5"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(dir, "update.rolled_back", map[string]any{"to": "0.1.6"}); err != nil {
		t.Fatal(err)
	}
	got, err := journal.Drain(dir)
	if err != nil || len(got) != 2 || got[0].Kind != "update.applied" || got[0].Data["to"] != "0.1.5" || got[1].Kind != "update.rolled_back" || got[0].At.IsZero() {
		t.Fatalf("drain = %+v, %v", got, err)
	}
	if again, _ := journal.Drain(dir); len(again) != 0 {
		t.Fatalf("handed over twice: %+v", again)
	}

	// A drain that stopped after taking the file: its entries come next time,
	// together with new ones, and a torn last line is skipped.
	_ = journal.Append(dir, "update.applied", nil)
	if err := os.Rename(filepath.Join(dir, "update-events.jsonl"), filepath.Join(dir, "update-events.sending")); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "update-events.sending"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"kind":"upd`)
	_ = f.Close()
	_ = journal.Append(dir, "update.rolled_back", nil)
	got, err = journal.Drain(dir)
	if err != nil || len(got) != 2 || got[0].Kind != "update.applied" || got[1].Kind != "update.rolled_back" {
		t.Fatalf("after an interrupted drain: %+v, %v", got, err)
	}
}
