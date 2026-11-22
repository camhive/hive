package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/motion"
	"github.com/camhive/hive/internal/store"
)

// #157: auto-calm leaves a row in the action log, with the hour's episodes
// when it calms, and another when the camera steps back.
func TestAutoCalmIsInTheActionLog(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 11, 22, 13, 10, 0, 0, time.UTC)
	log := slog.New(slog.DiscardHandler)
	recordCalm(st, log, motion.Calming{Camera: "cam_a", Calmed: true, At: at, Episodes: 21, Sensitivity: camera.SensitivityLow})
	recordCalm(st, log, motion.Calming{Camera: "cam_a", At: at.Add(2 * time.Hour)})
	rows, err := st.Actions().Recent(ctx, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %+v, %v", rows, err)
	}
	var calmed, ended map[string]any
	_ = json.Unmarshal(rows[1].Details, &calmed)
	_ = json.Unmarshal(rows[0].Details, &ended)
	if rows[1].Action != "motion.calmed" || rows[1].Actor != "hive" || rows[1].Target != "cam_a" || !rows[1].At.Equal(at) ||
		calmed["episodes"] != float64(21) || calmed["sensitivity"] != "low" {
		t.Fatalf("calmed %+v %v", rows[1], calmed)
	}
	if rows[0].Action != "motion.calm_ended" || ended["sensitivity"] != "normal" || ended["episodes"] != nil {
		t.Fatalf("ended %+v %v", rows[0], ended)
	}
}
