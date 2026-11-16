package onvif

import (
	"encoding/xml"
	"testing"
)

func TestNotificationsAreReadAsMotionByTopicAndItem(t *testing.T) {
	msg := func(topic, name, value string) string {
		return `<wsnt:NotificationMessage xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:tt="http://www.onvif.org/ver10/schema">` +
			`<wsnt:Topic>` + topic + `</wsnt:Topic><wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="` + name + `" Value="` + value + `"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>`
	}
	for _, tc := range []struct {
		raw    string
		on, ok bool
	}{
		{msg("tns1:VideoSource/MotionAlarm", "State", "true"), true, true},
		{msg("tns1:RuleEngine/CellMotionDetector/Motion", "IsMotion", "false"), false, true},
		{msg("tns1:RuleEngine/MyRuleDetector/Motion", "Motion", "1"), true, true},
		{msg("tns1:VideoSource/ImageTooDark", "State", "true"), false, false},
		{msg("tns1:VideoSource/MotionAlarm", "Level", "7"), false, false},
	} {
		var n notification
		if err := xml.Unmarshal([]byte(tc.raw), &n); err != nil {
			t.Fatal(err)
		}
		if on, ok := n.motion(); on != tc.on || ok != tc.ok {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", n.Topic, on, ok, tc.on, tc.ok)
		}
	}
}

// Cameras that announce services only through GetCapabilities (or only some
// through GetServices) still get every address we know.
func TestServiceAddressesFillGapsFromTheOtherAnswer(t *testing.T) {
	services := xaddrs{media: "m1", events: ""}
	caps := xaddrs{media: "m2", ptz: "p2", events: "e2"}
	if got := services.or(caps); got != (xaddrs{media: "m1", ptz: "p2", events: "e2"}) {
		t.Fatalf("got %+v", got)
	}
	if got := (xaddrs{media: "m", ptz: "p", events: "e"}).or(caps); got != (xaddrs{media: "m", ptz: "p", events: "e"}) {
		t.Fatalf("known addresses must win: %+v", got)
	}
}
