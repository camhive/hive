package camsim_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
)

func TestScenarioHalvesTheBitrateAndDropsDuringTrouble(t *testing.T) {
	s := camsim.Scenario{Kbps: 4000, Trouble: time.Hour, Recover: 2 * time.Hour, BitrateFactor: 0.5, ReconnectEvery: 5 * time.Minute}
	cases := []struct {
		at   time.Duration
		kbps int
		up   bool
	}{
		{0, 4000, true},
		{time.Hour, 2000, true},
		{time.Hour + 4*time.Minute + 49*time.Second, 2000, true},
		{time.Hour + 4*time.Minute + 50*time.Second, 2000, false}, // the last 10 s of every 5 minutes
		{time.Hour + 5*time.Minute, 2000, true},
		{2 * time.Hour, 4000, true},
	}
	for _, c := range cases {
		if got, up := s.KbpsAt(c.at), s.UpAt(c.at); got != c.kbps || up != c.up {
			t.Errorf("at %s: %d kbps up %v, want %d %v", c.at, got, up, c.kbps, c.up)
		}
	}
}
