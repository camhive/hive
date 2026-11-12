package hiveapp_test

import (
	"net/http"
	"testing"
)

// The web app lives on another origin (app.camhive.app) and reaches the hive
// directly on the LAN or via the relay; without CORS every browser call dies.
func TestCORSPreflightAndSimpleRequests(t *testing.T) {
	f := newAPI(t)
	req, _ := http.NewRequest(http.MethodOptions, f.srv.URL+"/v0/cameras", nil)
	req.Header.Set("Origin", "https://app.camhive.app")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status %d", res.StatusCode)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":          "*",
		"Access-Control-Allow-Headers":         "Authorization, Content-Type, Range",
		"Access-Control-Allow-Private-Network": "true",
	} {
		if got := res.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// The real request is still authenticated; CORS only lets the browser see the answer.
	res2, err := http.Get(f.srv.URL + "/v0/cameras")
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusUnauthorized || res2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unauthenticated GET: status %d, ACAO %q", res2.StatusCode, res2.Header.Get("Access-Control-Allow-Origin"))
	}
	res3, err := http.Get(f.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res3.Body.Close()
	if res3.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("healthz must be probe-able cross-origin")
	}
}
