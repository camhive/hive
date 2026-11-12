package retention

import "testing"

func TestLicensedCapWinsOverSettings(t *testing.T) {
	seven := func() int { return 7 }
	for _, c := range []struct {
		p    Policy
		cam  string
		want int
	}{
		{Policy{DefaultDays: 30, MaxDays: seven}, "a", 7},
		{Policy{DefaultDays: 0, MaxDays: seven}, "a", 7}, // unlimited is capped too
		{Policy{DefaultDays: 3, MaxDays: seven}, "a", 3},
		{Policy{DefaultDays: 30, DaysPerCamera: map[string]int{"a": 90}, MaxDays: seven}, "a", 7},
		{Policy{DefaultDays: 30, MaxDays: func() int { return -1 }}, "a", 30},
		{Policy{DefaultDays: 30}, "a", 30},
	} {
		if got := c.p.days(c.cam); got != c.want {
			t.Fatalf("%+v → %d want %d", c.p, got, c.want)
		}
	}
}
