package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/store"
)

// A degraded episode is one lasting camera.degraded Event, sent when it
// begins, however often care repeats it; its end closes the Event and
// sends camera.recovered, unless the camera was switched off (#163).
func TestDegradedEpisodesAreOneEventAndARecovery(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var sent []store.Event
	ep := newDegradedEpisodes(st.Events(), func(e store.Event) { sent = append(sent, e) }, nil)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ep.begin("cam_a", at, []health.Reason{health.BitrateDrop, health.Reconnects})
	ep.begin("cam_a", at, []health.Reason{health.BitrateDrop}) // the next check: still the one episode
	ep.end("cam_a", at.Add(2*time.Hour), true)
	ep.end("cam_b", at.Add(2*time.Hour), true) // nothing open: nothing sent
	ep.begin("cam_c", at, []health.Reason{health.ClockDrift})
	ep.end("cam_c", at.Add(time.Hour), false) // disabled: closed, no recovery sent

	rows, err := st.Events().Range(ctx, "cam_a", at.Add(-time.Hour), at.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Reasons []string }
	if len(rows) != 1 || rows[0].Kind != health.EventDegraded || !rows[0].End.Equal(at.Add(2*time.Hour)) ||
		json.Unmarshal(rows[0].Data, &data) != nil || len(data.Reasons) != 2 || data.Reasons[0] != "bitrate_drop" {
		t.Fatalf("rows %+v", rows)
	}
	if len(sent) != 3 || sent[0].Kind != health.EventDegraded || sent[0].ID != rows[0].ID ||
		sent[1].Kind != health.EventRecovered || sent[1].Camera != "cam_a" || !sent[1].At.Equal(at.Add(2*time.Hour)) ||
		sent[2].Kind != health.EventDegraded || sent[2].Camera != "cam_c" {
		t.Fatalf("sent %+v", sent)
	}
	if c, _ := st.Events().Range(ctx, "cam_c", at.Add(-time.Hour), at.Add(3*time.Hour)); len(c) != 1 || c[0].End.IsZero() {
		t.Fatalf("disabled camera's episode %+v", c)
	}
}
