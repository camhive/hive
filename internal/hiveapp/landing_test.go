package hiveapp_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveapp"
)

const landingVersion = "9.9.9-landing"

// landingServer serves the hive API with the given connection marking:
// hiveapp.LANConn for the LAN TLS listener, hiveapp.RelayedConn for the
// relay, nil for the loopback listener.
func landingServer(t *testing.T, mark func(context.Context, net.Conn) context.Context) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(hiveapp.NewAPI(hiveapp.Deps{Version: landingVersion}))
	srv.Config.ConnContext = mark
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func getLanding(t *testing.T, url, acceptLanguage string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/", nil)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(body)
}

// ADR-0023: in an emergency the user opens the hive at its LAN address in a
// new tab and proceeds through the browser's warning; the page that loads
// then tells them, in their language, to go back to the app.
func TestLandingPageSendsTheUserBackToTheApp(t *testing.T) {
	lan := landingServer(t, hiveapp.LANConn)
	for _, tc := range []struct{ accept, want string }{
		{"", "Your browser can now reach this hive"},
		{"en-US,en;q=0.9,de;q=0.8", "Your browser can now reach this hive"},
		{"de-DE,de;q=0.9,en;q=0.8", "Ihr Browser erreicht diesen Hive jetzt"},
		{"de", "Ihr Browser erreicht diesen Hive jetzt"},
		{"DE-AT;q=1", "Ihr Browser erreicht diesen Hive jetzt"},
		{"dev-x,de;q=0.9", "Your browser can now reach this hive"},
		{"fr-FR,fr;q=0.9", "Your browser can now reach this hive"},
	} {
		status, ctype, body := getLanding(t, lan, tc.accept)
		if status != http.StatusOK || !strings.HasPrefix(ctype, "text/html") || !strings.Contains(body, tc.want) {
			t.Fatalf("Accept-Language %q: %d %s %s", tc.accept, status, ctype, body)
		}
	}
}

// The page needs no token, so it says nothing about the hive: no version,
// no id or name, no links into the API, no script.
func TestLandingPageCarriesNothingAboutTheHive(t *testing.T) {
	lan := landingServer(t, hiveapp.LANConn)
	for _, lang := range []string{"en", "de"} {
		_, _, body := getLanding(t, lan, lang)
		for _, leak := range []string{landingVersion, "hv_", "hv-", "/v0", "/healthz", "<a", "href", "<script", "src="} {
			if strings.Contains(body, leak) {
				t.Errorf("%s page contains %q:\n%s", lang, leak, body)
			}
		}
	}
}

// The page exists only on the LAN listener: through the relay and on the
// loopback listener the root is an ordinary JSON 404.
func TestLandingPageOnlyOnTheLANListener(t *testing.T) {
	for name, mark := range map[string]func(context.Context, net.Conn) context.Context{"relay": hiveapp.RelayedConn, "loopback": nil} {
		status, ctype, body := getLanding(t, landingServer(t, mark), "")
		if status != http.StatusNotFound || !strings.HasPrefix(ctype, "application/json") || !strings.Contains(body, "not_found") {
			t.Errorf("%s: %d %s %s", name, status, ctype, body)
		}
	}
}

// The landing page is the root only; an unknown path stays a 404.
func TestLandingPageIsOnlyTheRoot(t *testing.T) {
	res, err := http.Get(landingServer(t, hiveapp.LANConn) + "/nothing-here")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path: %d", res.StatusCode)
	}
}

// The page is HTML on an origin that holds the owner's local session: it
// asks browsers not to guess another type and not to keep it.
func TestLandingPageForbidsSniffingAndCaching(t *testing.T) {
	res, err := http.Get(landingServer(t, hiveapp.LANConn) + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", res.Header)
	}
}
