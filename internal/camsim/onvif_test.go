package camsim

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func soap(t *testing.T, srv *httptest.Server, path, body string) string {
	t.Helper()
	env := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>` + body + `</s:Body></s:Envelope>`
	resp, err := http.Post(srv.URL+path, "application/soap+xml", strings.NewReader(env))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d\n%s", path, resp.StatusCode, b)
	}
	return string(b)
}

func newONVIFServer(t *testing.T) (*httptest.Server, Config) {
	t.Helper()
	cfg := testConfig()
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	t.Cleanup(srv.Close)
	return srv, cfg
}

func TestONVIFGetDeviceInformation(t *testing.T) {
	srv, _ := newONVIFServer(t)
	out := soap(t, srv, "/onvif/device_service", `<tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)
	for _, want := range []string{"GetDeviceInformationResponse", "<tds:Manufacturer>Camhive</tds:Manufacturer>", "<tds:Model>camsim</tds:Model>", "SerialNumber"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestONVIFGetCapabilitiesPointsToMediaService(t *testing.T) {
	srv, cfg := newONVIFServer(t)
	out := soap(t, srv, "/onvif/device_service", `<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl"><tds:Category>All</tds:Category></tds:GetCapabilities>`)
	// Like a real camera, the mock advertises its configured address, not the
	// socket it happens to be reached on.
	if !strings.Contains(out, "<tt:XAddr>"+cfg.mediaServiceURL()+"</tt:XAddr>") {
		t.Fatalf("media XAddr missing in\n%s", out)
	}
}

func TestONVIFGetProfilesListsEveryCameraStream(t *testing.T) {
	srv, cfg := newONVIFServer(t)
	out := soap(t, srv, "/onvif/media_service", `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`)
	for _, cam := range cfg.Cameras {
		for _, s := range []Stream{Main, Sub} {
			if !strings.Contains(out, `token="`+profileToken(cam, s)+`"`) {
				t.Errorf("profile for %s/%s missing", cam.Name, s)
			}
		}
	}
	if !strings.Contains(out, "<tt:Encoding>H264</tt:Encoding>") || !strings.Contains(out, "<tt:Encoding>H265</tt:Encoding>") {
		t.Errorf("encodings missing in\n%s", out)
	}
	if !strings.Contains(out, "<tt:Width>1920</tt:Width>") {
		t.Errorf("resolution missing")
	}
}

func TestONVIFGetStreamUriReturnsRTSPForProfile(t *testing.T) {
	srv, cfg := newONVIFServer(t)
	cam := cfg.Cameras[1]
	out := soap(t, srv, "/onvif/media_service", `<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>`+profileToken(cam, Sub)+`</trt:ProfileToken></trt:GetStreamUri>`)
	if !strings.Contains(out, "<tt:Uri>"+cfg.RTSPURL(cam, Sub)+"</tt:Uri>") {
		t.Fatalf("stream uri missing in\n%s", out)
	}
}

func TestONVIFUnknownProfileIsSOAPFault(t *testing.T) {
	srv, _ := newONVIFServer(t)
	env := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>nope</trt:ProfileToken></trt:GetStreamUri></s:Body></s:Envelope>`
	resp, err := http.Post(srv.URL+"/onvif/media_service", "application/soap+xml", strings.NewReader(env))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "NoProfile") {
		t.Fatalf("want 400 with NoProfile fault, got %d\n%s", resp.StatusCode, b)
	}
}

func TestONVIFGetSystemDateAndTimeIsUTC(t *testing.T) {
	srv, _ := newONVIFServer(t)
	out := soap(t, srv, "/onvif/device_service", `<tds:GetSystemDateAndTime xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)
	if !strings.Contains(out, "<tt:UTCDateTime>") {
		t.Fatalf("UTCDateTime missing in\n%s", out)
	}
}

func TestONVIFUnknownActionIsFault(t *testing.T) {
	srv, _ := newONVIFServer(t)
	env := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><x:Whatever xmlns:x="urn:x"/></s:Body></s:Envelope>`
	resp, err := http.Post(srv.URL+"/onvif/device_service", "application/soap+xml", strings.NewReader(env))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}
