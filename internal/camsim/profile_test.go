package camsim

import (
	"crypto/sha1" //nolint:gosec // WS-UsernameToken digest is defined over SHA-1
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// usernameToken builds a WS-Security UsernameToken header the way an ONVIF
// client does: digest = Base64(SHA1(nonce + created + password)).
func usernameTokenHeader(user, pass string) string {
	nonce := []byte("0123456789abcdef")
	created := time.Now().UTC().Format(time.RFC3339)
	h := sha1.New() //nolint:gosec // see above
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(pass))
	return `<s:Header><Security xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" s:mustUnderstand="1">` +
		`<UsernameToken><Username>` + user + `</Username>` +
		`<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + base64.StdEncoding.EncodeToString(h.Sum(nil)) + `</Password>` +
		`<Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` + base64.StdEncoding.EncodeToString(nonce) + `</Nonce>` +
		`<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + created + `</Created>` +
		`</UsernameToken></Security></s:Header>`
}

func postSOAP(t *testing.T, url, header, body string) (int, string) {
	t.Helper()
	env := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">` + header + `<s:Body>` + body + `</s:Body></s:Envelope>`
	resp, err := http.Post(url, "application/soap+xml", strings.NewReader(env))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const getProfiles = `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`

func TestAuthRequiredWhenCredentialsConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.Username, cfg.Password = "admin", "hunter2"
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	defer srv.Close()

	status, out := postSOAP(t, srv.URL+"/onvif/media_service", "", getProfiles)
	if status != http.StatusUnauthorized || !strings.Contains(out, "NotAuthorized") {
		t.Fatalf("no credentials: status %d\n%s", status, out)
	}
	status, _ = postSOAP(t, srv.URL+"/onvif/media_service", usernameTokenHeader("admin", "wrong"), getProfiles)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d", status)
	}
	status, out = postSOAP(t, srv.URL+"/onvif/media_service", usernameTokenHeader("admin", "hunter2"), getProfiles)
	if status != http.StatusOK || !strings.Contains(out, "GetProfilesResponse") {
		t.Fatalf("valid digest: status %d\n%s", status, out)
	}
}

func TestNoAuthRequiredWithoutCredentials(t *testing.T) {
	cfg := testConfig()
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	defer srv.Close()
	if status, _ := postSOAP(t, srv.URL+"/onvif/media_service", "", getProfiles); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
}

func TestQuirkyProfileOmitsMediaXAddrButGetServicesHasIt(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = Quirky
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	defer srv.Close()

	_, caps := postSOAP(t, srv.URL+"/onvif/device_service", "", `<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)
	if strings.Contains(caps, "<tt:Media>") {
		t.Fatalf("quirky GetCapabilities must omit Media:\n%s", caps)
	}
	_, svcs := postSOAP(t, srv.URL+"/onvif/device_service", "", `<tds:GetServices xmlns:tds="http://www.onvif.org/ver10/device/wsdl"><tds:IncludeCapability>false</tds:IncludeCapability></tds:GetServices>`)
	if !strings.Contains(svcs, "http://www.onvif.org/ver10/media/wsdl") || !strings.Contains(svcs, cfg.mediaServiceURL()) {
		t.Fatalf("GetServices must list the media service:\n%s", svcs)
	}
}

func TestQuirkyStreamURICarriesQueryAndProfileSelectsPath(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = Quirky
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	defer srv.Close()
	cam := cfg.Cameras[0]
	_, out := postSOAP(t, srv.URL+"/onvif/media_service", "", `<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>`+profileToken(cam, Main)+`</trt:ProfileToken></trt:GetStreamUri>`)
	want := cfg.RTSPURL(cam, Main) + "?profile=" + profileToken(cam, Main)
	if !strings.Contains(out, "<tt:Uri>"+want+"</tt:Uri>") {
		t.Fatalf("quirky stream uri should be %s:\n%s", want, out)
	}
}

func TestQuirkyProfileIsSlow(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = Quirky
	cfg.quirkDelay = 150 * time.Millisecond
	srv := httptest.NewServer(newONVIFHandler(&cfg))
	defer srv.Close()
	start := time.Now()
	postSOAP(t, srv.URL+"/onvif/device_service", "", `<tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("quirky responses must be delayed")
	}
}

func TestGetServicesIncludesPTZAndPTZMockRecordsCommands(t *testing.T) {
	cfg := testConfig()
	ptz := newPTZLog()
	srv := httptest.NewServer(newONVIFHandlerWithPTZ(&cfg, ptz))
	defer srv.Close()
	cam := cfg.Cameras[0]
	tok := profileToken(cam, Main)

	_, svcs := postSOAP(t, srv.URL+"/onvif/device_service", "", `<tds:GetServices xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)
	if !strings.Contains(svcs, "http://www.onvif.org/ver20/ptz/wsdl") {
		t.Fatalf("GetServices must list PTZ:\n%s", svcs)
	}
	status, _ := postSOAP(t, srv.URL+"/onvif/ptz_service", "", `<tptz:ContinuousMove xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><tptz:ProfileToken>`+tok+`</tptz:ProfileToken><tptz:Velocity><tt:PanTilt x="0.5" y="-0.25"/><tt:Zoom x="0.1"/></tptz:Velocity></tptz:ContinuousMove>`)
	if status != http.StatusOK {
		t.Fatalf("ContinuousMove status %d", status)
	}
	postSOAP(t, srv.URL+"/onvif/ptz_service", "", `<tptz:Stop xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>`+tok+`</tptz:ProfileToken><tptz:PanTilt>true</tptz:PanTilt><tptz:Zoom>true</tptz:Zoom></tptz:Stop>`)
	postSOAP(t, srv.URL+"/onvif/ptz_service", "", `<tptz:GotoPreset xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>`+tok+`</tptz:ProfileToken><tptz:PresetToken>preset_2</tptz:PresetToken></tptz:GotoPreset>`)
	_, presets := postSOAP(t, srv.URL+"/onvif/ptz_service", "", `<tptz:GetPresets xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>`+tok+`</tptz:ProfileToken></tptz:GetPresets>`)
	if !strings.Contains(presets, `token="preset_1"`) || !strings.Contains(presets, `token="preset_3"`) {
		t.Fatalf("GetPresets:\n%s", presets)
	}

	got := ptz.Commands()
	want := []PTZCommand{
		{Profile: tok, Action: "ContinuousMove", Pan: 0.5, Tilt: -0.25, Zoom: 0.1},
		{Profile: tok, Action: "Stop"},
		{Profile: tok, Action: "GotoPreset", Preset: "preset_2"},
	}
	if len(got) != len(want) {
		t.Fatalf("commands = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPTZUnknownProfileIsFault(t *testing.T) {
	cfg := testConfig()
	srv := httptest.NewServer(newONVIFHandlerWithPTZ(&cfg, newPTZLog()))
	defer srv.Close()
	status, out := postSOAP(t, srv.URL+"/onvif/ptz_service", "", `<tptz:Stop xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"><tptz:ProfileToken>nope</tptz:ProfileToken></tptz:Stop>`)
	if status != http.StatusBadRequest || !strings.Contains(out, "NoProfile") {
		t.Fatalf("status %d\n%s", status, out)
	}
}

func TestProfileValidation(t *testing.T) {
	cfg := testConfig()
	cfg.Profile = "weird"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown profile must be rejected")
	}
	cfg.Profile = ""
	cfg.applyDefaults()
	if cfg.Profile != Normal {
		t.Fatalf("default profile = %q", cfg.Profile)
	}
}
