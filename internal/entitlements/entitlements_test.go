package entitlements_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	ent "github.com/camhive/hive/internal/entitlements"
)

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func TestRegistryAndSeedPlansAreConsistent(t *testing.T) {
	reg := ent.Registry()
	if len(reg) < 10 {
		t.Fatal("registry too small")
	}
	seen := map[ent.Key]bool{}
	for _, d := range reg {
		if seen[d.Key] {
			t.Fatalf("duplicate key %s", d.Key)
		}
		seen[d.Key] = true
		if err := (ent.Set{d.Key: d.Default}).Validate(); err != nil {
			t.Fatalf("default of %s invalid: %v", d.Key, err)
		}
		if _, ok := ent.Lookup(d.Key); !ok {
			t.Fatalf("lookup %s", d.Key)
		}
	}
	if _, ok := ent.Lookup("nope"); ok {
		t.Fatal("unknown key found")
	}
	for _, p := range ent.Plans() {
		if err := p.Entitlements.Validate(); err != nil {
			t.Fatalf("plan %s: %v", p.ID, err)
		}
		if len(p.Entitlements) != len(reg) {
			t.Fatalf("plan %s must set every key (%d of %d)", p.ID, len(p.Entitlements), len(reg))
		}
	}
	for _, a := range ent.AddOns() {
		if err := a.Entitlements.Validate(); err != nil {
			t.Fatalf("add-on %s: %v", a.ID, err)
		}
	}
	free, _ := ent.PlanByID(ent.PlanFree)
	plus, _ := ent.PlanByID(ent.PlanPlus)
	pro, _ := ent.PlanByID(ent.PlanPro)
	if _, ok := ent.PlanByID("gold"); ok {
		t.Fatal("unknown plan")
	}
	// Tiers only go up.
	for _, k := range []ent.Key{ent.HivesMax, ent.SharesMax, ent.SessionsConcurrent} {
		if free.Entitlements.Int(k) >= plus.Entitlements.Int(k) || plus.Entitlements.Int(k) >= pro.Entitlements.Int(k) {
			t.Fatalf("%s must increase across tiers", k)
		}
	}
	if free.Entitlements.Str(ent.RemoteQuality) != "sub" || !plus.Entitlements.AtLeast(ent.RemoteQuality, "full") {
		t.Fatal("remote quality tiers")
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]ent.Set{
		"unknown key":   {"x.y": ent.I(1)},
		"negative int":  {ent.HivesMax: ent.I(-5)},
		"string in int": {ent.HivesMax: ent.S("many")},
		"int in bool":   {ent.ExportAPI: ent.I(1)},
		"bad enum":      {ent.RemoteQuality: ent.S("ultra")},
	}
	for name, s := range cases {
		if err := s.Validate(); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
	if err := (ent.Set{ent.HivesMax: ent.I(ent.Unlimited), ent.ExportAPI: ent.B(true), ent.RemoteQuality: ent.S("full")}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaultsPlanAddOnsAndPromos(t *testing.T) {
	free, _ := ent.PlanByID(ent.PlanFree)
	pro, _ := ent.PlanByID(ent.PlanPro)
	extra := ent.AddOn{ID: "hive.extra", Entitlements: ent.Set{ent.HivesMax: ent.I(1)}}
	quality := ent.AddOn{ID: "quality", Entitlements: ent.Set{ent.RemoteQuality: ent.S("full")}}
	lower := ent.AddOn{ID: "weird", Entitlements: ent.Set{ent.RemoteQuality: ent.S("sub"), ent.PTZEnabled: ent.B(false), ent.SessionsConcurrent: ent.I(0)}}
	unlimited := ent.AddOn{ID: "unl", Entitlements: ent.Set{ent.ExportRemotePerDay: ent.I(ent.Unlimited)}}
	promo := ent.Promo{ID: "launch", Entitlements: ent.Set{ent.SharesMax: ent.I(3), ent.AnalyticsLevel: ent.S("objects")}, ValidFrom: t0.Add(-time.Hour), ValidUntil: t0.Add(time.Hour)}
	expired := ent.Promo{ID: "old", Entitlements: ent.Set{ent.SharesMax: ent.I(100)}, ValidFrom: t0.Add(-48 * time.Hour), ValidUntil: t0.Add(-24 * time.Hour)}
	future := ent.Promo{ID: "soon", Entitlements: ent.Set{ent.SharesMax: ent.I(100)}, ValidFrom: t0.Add(24 * time.Hour)}
	forever := ent.Promo{ID: "forever", Entitlements: ent.Set{ent.UpdatesEarlyAccess: ent.B(true)}, ValidFrom: t0.Add(-time.Hour)}

	cases := []struct {
		name   string
		plan   ent.Plan
		addons []ent.AddOnGrant
		promos []ent.Promo
		want   map[ent.Key]ent.Value
	}{
		{"empty plan gets defaults", ent.Plan{ID: "none"}, nil, nil,
			map[ent.Key]ent.Value{ent.HivesMax: ent.I(1), ent.SessionsConcurrent: ent.I(1), ent.RemoteQuality: ent.S("sub"), ent.PTZEnabled: ent.B(true)}},
		{"plan values win over defaults", pro, nil, nil,
			map[ent.Key]ent.Value{ent.HivesMax: ent.I(10), ent.AnalyticsLevel: ent.S("full"), ent.ExportAPI: ent.B(true)}},
		{"add-ons add by quantity", pro, []ent.AddOnGrant{{AddOn: extra, Quantity: 3}}, nil,
			map[ent.Key]ent.Value{ent.HivesMax: ent.I(13)}},
		{"conflicting add-ons resolve upward, never lower", free, []ent.AddOnGrant{{AddOn: quality, Quantity: 1}, {AddOn: lower, Quantity: 1}}, nil,
			map[ent.Key]ent.Value{ent.RemoteQuality: ent.S("full"), ent.PTZEnabled: ent.B(true), ent.SessionsConcurrent: ent.I(2)}},
		{"unlimited absorbs additions", free, []ent.AddOnGrant{{AddOn: unlimited, Quantity: 1}, {AddOn: ent.AddOn{Entitlements: ent.Set{ent.ExportRemotePerDay: ent.I(2)}}, Quantity: 1}}, nil,
			map[ent.Key]ent.Value{ent.ExportRemotePerDay: ent.I(ent.Unlimited)}},
		{"active promo applies, expired and future do not", free, nil, []ent.Promo{promo, expired, future, forever},
			map[ent.Key]ent.Value{ent.SharesMax: ent.I(5), ent.AnalyticsLevel: ent.S("objects"), ent.UpdatesEarlyAccess: ent.B(true)}},
		{"zero quantity add-on is a no-op", free, []ent.AddOnGrant{{AddOn: extra, Quantity: 0}}, nil,
			map[ent.Key]ent.Value{ent.HivesMax: ent.I(1)}},
		{"unknown keys in plan or add-on are ignored", ent.Plan{Entitlements: ent.Set{"bogus": ent.I(9)}}, []ent.AddOnGrant{{AddOn: ent.AddOn{Entitlements: ent.Set{"bogus": ent.I(9)}}, Quantity: 1}}, nil,
			map[ent.Key]ent.Value{ent.HivesMax: ent.I(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ent.Resolve(c.plan, c.addons, c.promos, t0)
			if len(got) != len(ent.Registry()) {
				t.Fatalf("resolved set must hold every key, has %d", len(got))
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
			for k, want := range c.want {
				if got[k] != want {
					t.Fatalf("%s = %v, want %v", k, got[k], want)
				}
			}
			if _, ok := got["bogus"]; ok {
				t.Fatal("unknown key leaked into the result")
			}
		})
	}
}

func TestAccessorsAllowsAtLeastAndJSON(t *testing.T) {
	plus, _ := ent.PlanByID(ent.PlanPlus)
	s := ent.Resolve(plus, nil, nil, t0)
	if !s.Allows(ent.SessionsConcurrent, 4) || s.Allows(ent.SessionsConcurrent, 5) || !s.Allows(ent.ExportRemotePerDay, 1_000_000) {
		t.Fatal("Allows")
	}
	if !s.AtLeast(ent.AnalyticsLevel, "objects") || s.AtLeast(ent.AnalyticsLevel, "full") || !s.AtLeast(ent.AnalyticsLevel, "motion") {
		t.Fatal("AtLeast")
	}
	if !s.Bool(ent.PTZEnabled) || s.Bool(ent.ExportAPI) {
		t.Fatal("Bool")
	}
	empty := ent.Set{}
	if empty.Int(ent.HivesMax) != 1 || empty.Str(ent.RemoteQuality) != "sub" || !empty.Bool(ent.PTZEnabled) {
		t.Fatal("missing keys fall back to defaults")
	}
	for _, v := range []struct {
		v    ent.Value
		want string
	}{{ent.I(3), "3"}, {ent.I(ent.Unlimited), "unlimited"}, {ent.B(true), "true"}, {ent.S("full"), "full"}} {
		if v.v.String() != v.want {
			t.Fatalf("%v → %q", v.v, v.v.String())
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back ent.Set
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for k, v := range s {
		if back[k] != v {
			t.Fatalf("round trip %s: %v != %v", k, back[k], v)
		}
	}
	for _, bad := range []string{`{"x.y":1}`, `{"hives.max":"many"}`, `{"hives.max":1.5}`, `{"export.api":1}`, `{"remote.quality":2}`, `{"remote.quality":"ultra"}`, `[1]`} {
		var s ent.Set
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Fatalf("%s must fail", bad)
		}
	}
	var s2 ent.Set
	if err := json.Unmarshal([]byte(`{"x.y":1}`), &s2); !errors.Is(err, ent.ErrUnknownKey) {
		t.Fatalf("unknown key error: %v", err)
	}
	p := ent.Promo{ValidFrom: t0}
	if !p.Active(t0) || p.Active(t0.Add(-time.Second)) {
		t.Fatal("promo without end")
	}
}
