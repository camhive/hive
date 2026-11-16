package camsim

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventsServiceIsAdvertisedUnlessTheCameraHasNone(t *testing.T) {
	srv, _ := newONVIFServer(t)
	for _, call := range []string{`<tds:GetServices xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`, `<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`} {
		if out := soap(t, srv, "/onvif/device_service", call); !strings.Contains(out, "/onvif/events_service") {
			t.Errorf("events service missing from\n%s", out)
		}
	}
	cfg := testConfig()
	cfg.NoEvents = true
	h := newONVIFHandler(&cfg)
	srv2 := serve(t, h)
	if out := soap(t, srv2, "/onvif/device_service", `<tds:GetServices xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`); strings.Contains(out, "events_service") {
		t.Errorf("NoEvents must hide the events service\n%s", out)
	}
}

func TestPullPointReportsInitialStateThenChanges(t *testing.T) {
	cfg := testConfig()
	cfg.Cameras[0].Motion = 3 * time.Second
	srv := serve(t, newONVIFHandler(&cfg))
	sub := soap(t, srv, "/onvif/events_service", `<tev:CreatePullPointSubscription xmlns:tev="http://www.onvif.org/ver10/events/wsdl"/>`)
	addr := textOf(sub, "Address")
	if !strings.Contains(addr, "/onvif/subscription/") {
		t.Fatalf("subscription address %q in\n%s", addr, sub)
	}
	path := addr[strings.Index(addr, "/onvif/"):]
	pull := `<tev:PullMessages xmlns:tev="http://www.onvif.org/ver10/events/wsdl"><tev:Timeout>PT5S</tev:Timeout><tev:MessageLimit>10</tev:MessageLimit></tev:PullMessages>`
	first := soap(t, srv, path, pull)
	if !strings.Contains(first, `Name="IsMotion" Value="false"`) || !strings.Contains(first, "CellMotionDetector/Motion") {
		t.Fatalf("initial state must be reported\n%s", first)
	}
	start := time.Now()
	second := soap(t, srv, path, pull) // long-polls until the object starts moving (2 s in)
	if !strings.Contains(second, `Name="IsMotion" Value="true"`) || time.Since(start) < time.Second {
		t.Fatalf("the change must arrive when it happens (after %v)\n%s", time.Since(start), second)
	}
	soap(t, srv, path, `<wsnt:Renew xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"><wsnt:TerminationTime>PT60S</wsnt:TerminationTime></wsnt:Renew>`)
	soap(t, srv, path, `<wsnt:Unsubscribe xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"/>`)
}

func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}
