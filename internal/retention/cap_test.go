package retention

import "testing"

func TestLicensedCapWinsOverSettings(t *testing.T) {
	seven := func() int { return 7 }
	for _, c := range []struct {
		p    Policy
		own  map[string]int
		cam  string
		want int
	}{
		{Policy{DefaultDays: 30, MaxDays: seven}, nil, "a", 7},
		{Policy{DefaultDays: 0, MaxDays: seven}, nil, "a", 7}, // unlimited is capped too
		{Policy{DefaultDays: 3, MaxDays: seven}, nil, "a", 3},
		{Policy{DefaultDays: 30, MaxDays: seven}, map[string]int{"a": 90}, "a", 7},
		{Policy{DefaultDays: 30, MaxDays: seven}, map[string]int{"a": 0}, "a", 7}, // a camera's "unlimited" too,
		{Policy{DefaultDays: 30, MaxDays: func() int { return -1 }}, nil, "a", 30},
		{Policy{DefaultDays: 30}, nil, "a", 30},
		{Policy{DefaultDays: 30}, map[string]int{"a": 90, "b": 2}, "a", 90},
		{Policy{DefaultDays: 30}, map[string]int{"b": 2}, "a", 30},
	} {
		if got := c.p.days(c.own, c.cam); got != c.want {
			t.Fatalf("%+v → %d want %d", c.p, got, c.want)
		}
	}
}
