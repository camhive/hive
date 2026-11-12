package update_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/camhive/hive/internal/update"
)

func TestVersionsCompareAsReleases(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.4", "0.1.3", 1}, {"0.1.10", "0.1.9", 1}, {"v1.0.0", "1.0.0", 0}, {"0.2.0-rc.1", "0.2.0", -1}, {"0.1.4", "0.2.0", -1},
	} {
		a, err := update.ParseVersion(tc.a)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := update.ParseVersion(tc.b)
		if got := a.Compare(b); got != tc.want {
			t.Errorf("%s vs %s = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, bad := range []string{"", "latest", "1.2", "0.1.4-dev+abc"} {
		if _, err := update.ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// ADR-0018 §7: a hive is in a rollout when its id falls in the first
// rollout percent; the share is close to the percentage and stable.
func TestRolloutTakesAStableShareOfHives(t *testing.T) {
	in := 0
	for i := range 10000 {
		id := fmt.Sprintf("hv_%04x", i)
		if update.InRollout(id, 10) {
			in++
			if !update.InRollout(id, 50) {
				t.Fatalf("%s left the rollout when it grew", id)
			}
		}
	}
	if in < 850 || in > 1150 {
		t.Fatalf("10%% rollout took %d of 10000", in)
	}
	if update.InRollout("hv_1", 0) || !update.InRollout("hv_1", 100) {
		t.Fatal("0 must take none, 100 all")
	}
}

func TestUpdateWindowCanCrossMidnight(t *testing.T) {
	w, err := update.ParseWindow("23:00-02:00")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 13, h, m, 0, 0, time.Local) }
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{{at(23, 30), true}, {at(1, 59), true}, {at(2, 0), false}, {at(12, 0), false}, {at(23, 0), true}} {
		if got := w.Contains(tc.t); got != tc.want {
			t.Errorf("%s: %v", tc.t.Format("15:04"), got)
		}
	}
	if d, _ := update.ParseWindow(""); !d.Contains(at(3, 0)) || d.Contains(at(6, 0)) {
		t.Fatal("the default window is 02:00-05:00")
	}
	for _, bad := range []string{"2-5", "25:00-01:00", "02:00-02:00", "02:00"} {
		if _, err := update.ParseWindow(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDecide(t *testing.T) {
	v := func(s string) update.Version { x, _ := update.ParseVersion(s); return x }
	night := time.Date(2026, 11, 13, 3, 0, 0, 0, time.Local)
	day := time.Date(2026, 11, 13, 14, 0, 0, 0, time.Local)
	w, _ := update.ParseWindow("")
	base := update.Situation{Hive: "hv_1", Running: v("0.1.4"), Updater: v("0.1.4"), Window: w, Mode: update.Automatic, Now: night}
	target := update.Target{Version: v("0.1.5"), Rollout: 100}
	for name, tc := range map[string]struct {
		sit    func(s update.Situation) update.Situation
		target func(t update.Target) update.Target
		want   update.Verdict
	}{
		"newer, in the window":         {nil, nil, update.Install},
		"same version":                 {nil, func(t update.Target) update.Target { t.Version = v("0.1.4"); return t }, update.UpToDate},
		"older":                        {nil, func(t update.Target) update.Target { t.Version = v("0.1.3"); return t }, update.UpToDate},
		"outside the window":           {func(s update.Situation) update.Situation { s.Now = day; return s }, nil, update.Wait},
		"security, outside the window": {func(s update.Situation) update.Situation { s.Now = day; return s }, func(t update.Target) update.Target { t.Security = true; return t }, update.Install},
		"not in the rollout":           {nil, func(t update.Target) update.Target { t.Rollout = 0; return t }, update.UpToDate},
		"manual":                       {func(s update.Situation) update.Situation { s.Mode = update.Manual; return s }, nil, update.Available},
		"manual, asked": {func(s update.Situation) update.Situation {
			s.Mode = update.Manual
			s.Asked = true
			s.Now = day
			return s
		}, nil, update.Install},
		"failed before":         {func(s update.Situation) update.Situation { s.Failed = []update.Version{v("0.1.5")}; return s }, nil, update.UpToDate},
		"needs a newer updater": {nil, func(t update.Target) update.Target { t.MinUpdater = v("0.2.0"); return t }, update.UpdaterTooOld},
	} {
		s, tg := base, target
		if tc.sit != nil {
			s = tc.sit(s)
		}
		if tc.target != nil {
			tg = tc.target(tg)
		}
		if got := update.Decide(s, tg); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
