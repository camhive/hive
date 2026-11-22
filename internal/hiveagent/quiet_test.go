package hiveagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/store"
)

// #157: a motion start sent while someone is at home carries quiet: true;
// the cloud then sends no push for it. Where anyone is never leaves the
// house, only that the event is quiet.
func TestMotionWhileAtHomeIsSentQuiet(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var home atomic.Bool
	ag := hiveagent.New(hiveagent.Deps{Store: st, Verifier: auth.NewVerifier("hv_1", nil), Version: "0.1.0", AtHome: home.Load})
	l := &cloudLink{connected: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx, l.emit, time.Hour)

	home.Store(true)
	ag.PromptStart(store.Event{ID: 1, Camera: "a", Kind: "motion", At: time.Now()})
	waitNames(t, l, 1)
	ag.EmitStart(store.Event{ID: 2, Kind: "storage.low", At: time.Now()})
	waitNames(t, l, 2)
	home.Store(false)
	ag.PromptStart(store.Event{ID: 3, Camera: "a", Kind: "motion", At: time.Now()})
	waitNames(t, l, 3)

	quiet := make([]any, 0, len(l.got))
	for _, e := range l.got {
		var m map[string]any
		_ = json.Unmarshal(e.Data, &m)
		quiet = append(quiet, m["quiet"])
	}
	if quiet[0] != true || quiet[1] != nil || quiet[2] != nil {
		t.Fatalf("quiet flags = %v", quiet)
	}
}
