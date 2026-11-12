// Package entitlements is the pure model of what a subscription allows
// (ADR-0006): a registry of typed keys with defaults, plans and add-ons as
// data, and Resolve, which folds plan, add-ons and promotions into one
// effective Set. Enforcement points elsewhere ask the Set by key; nothing
// here touches a database or the clock except through arguments.
package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Type is the value kind of a key.
type Type int

// Types.
const (
	Int  Type = iota // non-negative count; Unlimited means no cap
	Bool             // feature flag
	Enum             // one of an ordered list; later entries are "more"
)

// Unlimited is the Int value for "no cap".
const Unlimited = -1

// Key names one entitlement; the registry gives it a type and default.
type Key string

// Registered keys. Names are grep-able at every enforcement point.
const (
	HivesMax            Key = "hives.max"             // active hives per account
	MembersMax          Key = "members.max"           // additional account users (co-users), not counting the owner
	SharesMax           Key = "shares.max"            // sharing recipients (members with camera grants)
	SessionsConcurrent  Key = "sessions.concurrent"   // live access sessions per account and hive
	RemoteQuality       Key = "remote.quality"        // sub | full
	ExportRemotePerDay  Key = "export.remote_per_day" // remote exports per day
	ExportMaxMinutes    Key = "export.max_minutes"    // longest remote export
	ExportAPI           Key = "export.api"            // batch export over the API
	NotifyChannels      Key = "notify.channels"       // basic | rich | all
	NotifyDevices       Key = "notify.devices"        // push devices
	AnalyticsLevel      Key = "analytics.level"       // motion | objects | full
	PTZEnabled          Key = "ptz.enabled"           // PTZ control
	RetentionDaysMax    Key = "archive.retention_days_max"
	UpdatesEarlyAccess  Key = "updates.early_access"
	RemoteAccessEnabled Key = "remote.enabled" // remote access at all (sleeping hives lose it)
)

// Def describes a key.
type Def struct {
	Key     Key
	Type    Type
	Default Value
	Enum    []string // ordered for Enum keys
	Doc     string
}

// Value is one typed entitlement value.
type Value struct {
	Int  int
	Bool bool
	Str  string
}

// I builds a count value.
func I(n int) Value { return Value{Int: n} }

// B builds a flag value.
func B(b bool) Value { return Value{Bool: b} }

// S builds an enum value.
func S(s string) Value { return Value{Str: s} }

// String renders a value for logs and the admin.
func (v Value) String() string {
	switch {
	case v.Str != "":
		return v.Str
	case v.Bool:
		return "true"
	case v.Int == Unlimited:
		return "unlimited"
	default:
		return fmt.Sprint(v.Int)
	}
}

var registry = []Def{
	{HivesMax, Int, I(1), nil, "active hives per account"},
	{MembersMax, Int, I(0), nil, "additional account users besides the owner"},
	{SharesMax, Int, I(0), nil, "people cameras can be shared with"},
	{SessionsConcurrent, Int, I(1), nil, "live remote sessions per account and hive; a new one evicts the oldest"},
	{RemoteQuality, Enum, S("sub"), []string{"sub", "full"}, "remote live quality"},
	{ExportRemotePerDay, Int, I(5), nil, "remote exports per day"},
	{ExportMaxMinutes, Int, I(5), nil, "longest remote export in minutes"},
	{ExportAPI, Bool, B(false), nil, "batch export API"},
	{NotifyChannels, Enum, S("basic"), []string{"basic", "rich", "all"}, "notification channels"},
	{NotifyDevices, Int, I(1), nil, "push devices"},
	{AnalyticsLevel, Enum, S("motion"), []string{"motion", "objects", "full"}, "analytics on the hive"},
	{PTZEnabled, Bool, B(true), nil, "PTZ control"},
	{RetentionDaysMax, Int, I(Unlimited), nil, "cap on archive retention"},
	{UpdatesEarlyAccess, Bool, B(false), nil, "early-access update channel"},
	{RemoteAccessEnabled, Bool, B(true), nil, "remote access through the cloud"},
}

var defs = func() map[Key]Def {
	m := make(map[Key]Def, len(registry))
	for _, d := range registry {
		m[d.Key] = d
	}
	return m
}()

// Registry returns every key definition in a stable order.
func Registry() []Def { return append([]Def(nil), registry...) }

// Lookup returns a key's definition.
func Lookup(k Key) (Def, bool) {
	d, ok := defs[k]
	return d, ok
}

// Set maps keys to values. A resolved Set contains every registered key.
type Set map[Key]Value

// Errors.
var (
	ErrUnknownKey = errors.New("entitlements: unknown key")
	ErrBadValue   = errors.New("entitlements: value does not fit the key's type")
)

// Validate checks that every entry names a registered key with a value of
// the right type (enum values from the list, ints >= -1).
func (s Set) Validate() error {
	for k, v := range s {
		d, ok := defs[k]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownKey, k)
		}
		if !fits(d, v) {
			return fmt.Errorf("%w: %s=%v", ErrBadValue, k, v)
		}
	}
	return nil
}

// fits reports whether v is a well-formed value for d.
func fits(d Def, v Value) bool {
	switch d.Type {
	case Int:
		return v.Int >= Unlimited && v.Str == ""
	case Bool:
		return v.Str == "" && v.Int == 0
	default:
		return enumIndex(d, v.Str) >= 0
	}
}

func enumIndex(d Def, s string) int {
	for i, e := range d.Enum {
		if e == s {
			return i
		}
	}
	return -1
}

// Int returns a count; missing keys fall back to the default.
func (s Set) Int(k Key) int {
	if v, ok := s[k]; ok {
		return v.Int
	}
	return defs[k].Default.Int
}

// Bool returns a flag; missing keys fall back to the default.
func (s Set) Bool(k Key) bool {
	if v, ok := s[k]; ok {
		return v.Bool
	}
	return defs[k].Default.Bool
}

// Str returns an enum value; missing keys fall back to the default.
func (s Set) Str(k Key) string {
	if v, ok := s[k]; ok {
		return v.Str
	}
	return defs[k].Default.Str
}

// Allows reports whether n items fit under an Int key (Unlimited always fits).
func (s Set) Allows(k Key, n int) bool {
	limit := s.Int(k)
	return limit == Unlimited || n <= limit
}

// AtLeast reports whether an Enum key is at or above a level.
func (s Set) AtLeast(k Key, level string) bool {
	d := defs[k]
	return enumIndex(d, s.Str(k)) >= enumIndex(d, level)
}

// Plan is a named tier.
type Plan struct {
	ID           string
	Name         string
	Entitlements Set
}

// AddOn adds to a plan: Int values are added per unit, Bool values turn a
// feature on, Enum values raise the level. It can never lower anything.
type AddOn struct {
	ID           string
	Name         string
	Entitlements Set
}

// AddOnGrant is an add-on bought n times.
type AddOnGrant struct {
	AddOn    AddOn
	Quantity int
}

// Promo is a time-bound grant with add-on semantics.
type Promo struct {
	ID           string
	Entitlements Set
	ValidFrom    time.Time
	ValidUntil   time.Time // zero means no end
}

// Active reports whether the promo applies at t.
func (p Promo) Active(t time.Time) bool {
	return !t.Before(p.ValidFrom) && (p.ValidUntil.IsZero() || t.Before(p.ValidUntil))
}

// Resolve folds defaults, the plan, add-ons (by quantity) and active promos
// into the effective Set. Conflicts resolve upward: counts add (Unlimited
// wins), flags OR, enums take the highest level. Every registered key is
// present in the result.
func Resolve(plan Plan, addons []AddOnGrant, promos []Promo, now time.Time) Set {
	out := Set{}
	for _, d := range registry {
		out[d.Key] = d.Default
	}
	for k, v := range plan.Entitlements {
		if _, ok := defs[k]; ok {
			out[k] = v
		}
	}
	for _, g := range addons {
		for i := 0; i < g.Quantity; i++ {
			out.raise(g.AddOn.Entitlements)
		}
	}
	for _, p := range promos {
		if p.Active(now) {
			out.raise(p.Entitlements)
		}
	}
	return out
}

// raise applies add-on semantics of extra onto s.
func (s Set) raise(extra Set) {
	for k, v := range extra {
		d, ok := defs[k]
		if !ok {
			continue
		}
		cur := s[k]
		switch d.Type {
		case Int:
			switch {
			case cur.Int == Unlimited || v.Int == Unlimited:
				s[k] = I(Unlimited)
			default:
				s[k] = I(cur.Int + v.Int)
			}
		case Bool:
			s[k] = B(cur.Bool || v.Bool)
		case Enum:
			if enumIndex(d, v.Str) > enumIndex(d, cur.Str) {
				s[k] = v
			}
		}
	}
}

// MarshalJSON writes {"key": value} with native JSON types (for licenses
// and the admin API).
func (s Set) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	out := make(map[string]any, len(s))
	for _, k := range keys {
		v := s[Key(k)]
		switch defs[Key(k)].Type {
		case Bool:
			out[k] = v.Bool
		case Enum:
			out[k] = v.Str
		default:
			out[k] = v.Int
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads {"key": value}; unknown keys and wrong types fail.
func (s *Set) UnmarshalJSON(raw []byte) error {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	out := Set{}
	for k, v := range in {
		d, ok := defs[Key(k)]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownKey, k)
		}
		val, ok := fromJSON(d, v)
		if !ok {
			return fmt.Errorf("%w: %s", ErrBadValue, k)
		}
		out[Key(k)] = val
	}
	if err := out.Validate(); err != nil {
		return err
	}
	*s = out
	return nil
}

// fromJSON converts a decoded JSON scalar to a Value of d's type.
func fromJSON(d Def, v any) (Value, bool) {
	switch d.Type {
	case Int:
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) {
			return Value{}, false
		}
		return I(int(f)), true
	case Bool:
		b, ok := v.(bool)
		return B(b), ok
	default:
		str, ok := v.(string)
		return S(str), ok
	}
}

// Provider answers the effective entitlements of an account; enforcement
// points depend on this and name the key they check.
type Provider interface {
	For(ctx context.Context, accountID string) Set
}

// Fixed is a Provider returning one resolved Set for everyone (tests,
// single-plan deployments).
type Fixed struct{ Set Set }

// For implements Provider.
func (f Fixed) For(context.Context, string) Set { return f.Set }

// OnPlan is Fixed with a seed plan resolved (tests).
func OnPlan(id string) Fixed {
	p, _ := PlanByID(id)
	return Fixed{Set: Resolve(p, nil, nil, time.Time{})}
}
