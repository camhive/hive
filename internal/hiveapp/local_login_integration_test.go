package hiveapp_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
)

// #57 and ADR-0023, the emergency path end to end: a claimed hive serves its
// real listeners (loopback, LAN TLS, relay) with no cloud and no DNS
// anywhere. The owner had enabled the local login while online; now the
// cloud is gone and the offline grant has expired. The browser opens the
// hive at its LAN address: the hive's certificate for its LAN name fails
// only on the name there (the warning the user checks), and once accepted
// for that name the landing page, the local sign-in, cameras, live
// signalling and the archive work. The same token and the sign-in itself
// are refused through the relay, and repeated wrong passwords lock the
// source.
func TestEmergencyLocalLoginWithTheCloudGone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.5/door"}}, time.Now())
	if err := st.Cameras().Put(ctx, cam); err != nil {
		t.Fatal(err)
	}
	idx := archive.NewIndex(st)
	day := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	if err := idx.Upsert(ctx, archive.Segment{Camera: string(cam.ID), Stream: "main", Start: day, Duration: 10 * time.Minute, Bytes: 1, Path: "/x", Complete: true}); err != nil {
		t.Fatal(err)
	}
	cloud, _ := auth.NewIssuer("k1")
	h := hiveapp.NewAPI(hiveapp.Deps{
		Store: st, Index: idx, Playback: (&fakePlayback{}).fetch, Exports: export.NewManager(t.TempDir(), nil, st.Exports()), Version: "test", Location: time.UTC,
		Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": cloud.PublicKey()}), // licence never valid: offline grants are refused
		Local:    auth.NewLocalSessions("hv_1", time.Now),
		Engine:   newWHEPEngine(t),
	})
	lanIP := privateAddress(t) // a client on the LAN, not loopback
	const lanName = "hv-1.h.camhive.test"
	roots, lanCert := publiclyIssued(t, lanName)
	ports := testports.Get(t, 2)
	relay, err := hiveapp.NewRelayListener(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app := hiveapp.New(hiveapp.Config{ListenAddr: fmt.Sprintf("127.0.0.1:%d", ports[0]), TLSListenAddr: net.JoinHostPort(lanIP, fmt.Sprint(ports[1])),
		TLS: &tls.Config{Certificates: []tls.Certificate{lanCert}, MinVersion: tls.VersionTLS12}, Relay: relay, Handler: h})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- app.Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })

	address := net.JoinHostPort(lanIP, fmt.Sprint(ports[1]))
	lan := hiveClient{base: "https://" + address, c: browserAtAddress(roots, lanName)}
	viaRelay := hiveClient{base: "https://hive", c: tlsClient(func(ctx context.Context, _, _ string) (net.Conn, error) { return relay.Dial(ctx) })}
	lan.waitUp(t)

	// What the browser's warning says at the address: the issuer is trusted,
	// only the name differs, and the name is the hive's LAN name.
	var mismatch x509.HostnameError
	if _, err := tls.Dial("tcp", address, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}); !errors.As(err, &mismatch) || mismatch.Certificate.DNSNames[0] != lanName {
		t.Fatalf("certificate at the address: want a name mismatch for %s, got %v", lanName, err)
	}
	if res, err := lan.c.Get(lan.base + "/"); err != nil || res.StatusCode != 200 {
		t.Fatalf("landing page at the address: %v %v", res, err)
	} else {
		_ = res.Body.Close()
	}

	// Online, earlier: the owner enabled the local login with a cloud token.
	now := time.Now()
	owner, _ := cloud.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "s1", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now, Expiry: now.Add(time.Minute)})
	if s, b := viaRelay.call(t, "PUT", "/v0/local-login", owner, map[string]string{"username": "anna", "password": localPassword}); s != 200 {
		t.Fatalf("enable through the relay while online: %d %v", s, b)
	}

	// Now: the cloud is gone and the 7-day offline grant has run out.
	week := now.Add(-8 * 24 * time.Hour)
	stale, _ := cloud.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "s0", Cameras: []string{"*"}, Role: auth.RoleOwner, Offline: true, IssuedAt: week, Expiry: week.Add(auth.MaxOfflineTTL)})
	if s, b := lan.call(t, "GET", "/v0/cameras", stale, nil); s != 401 || b["code"] != "token_expired" {
		t.Fatalf("expired offline grant: %d %v", s, b)
	}

	s, b := lan.call(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword})
	tok, _ := b["token"].(string)
	if s != 201 || tok == "" {
		t.Fatalf("local sign-in on the LAN listener: %d %v", s, b)
	}
	if s, b := lan.call(t, "GET", "/v0/cameras", tok, nil); s != 200 || b["list"].([]any)[0].(map[string]any)["id"] != string(cam.ID) {
		t.Fatalf("cameras: %d %v", s, b)
	}
	if s, b := lan.call(t, "POST", "/v0/cameras/"+string(cam.ID)+"/live/offer", tok, map[string]string{"sdp": "v=0 offer", "stream": "main"}); s != 201 || b["sdp"] != "v=0 answer" {
		t.Fatalf("live signalling: %d %v", s, b)
	}
	if s, b := lan.call(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/calendar?stream=main", tok, nil); s != 200 || len(b["list"].([]any)) != 1 {
		t.Fatalf("archive calendar: %d %v", s, b)
	}
	if s, b := lan.call(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/timeline?stream=main&from=2026-11-09T10:00:00Z&to=2026-11-09T11:00:00Z", tok, nil); s != 200 || len(b["list"].([]any)) != 1 {
		t.Fatalf("archive timeline: %d %v", s, b)
	}

	if s, b := viaRelay.call(t, "GET", "/v0/cameras", tok, nil); s != 403 || b["code"] != "lan_only" {
		t.Fatalf("local token through the relay: %d %v", s, b)
	}
	if s, b := viaRelay.call(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword}); s != 403 || b["code"] != "lan_only" {
		t.Fatalf("sign-in through the relay: %d %v", s, b)
	}

	for range hiveapp.LocalLoginLockout.MaxFailures {
		if s, b := lan.call(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": "guess guess guess"}); s != 401 {
			t.Fatalf("wrong password: %d %v", s, b)
		}
	}
	if s, b := lan.call(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword}); s != 429 || b["code"] != "local_login_rate_limited" {
		t.Fatalf("after the rate limit: %d %v", s, b)
	}
}

// privateAddress is an RFC 1918 or ULA address of this machine, so that the
// test's client reaches the LAN listener the way a phone on the network
// does. Without one the test skips (CAMSIM_REQUIRE_TOOLS makes that fatal).
func privateAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsPrivate() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("no private IPv4 address on this machine (%v)", err)
	}
	t.Skipf("no private IPv4 address on this machine (%v)", err)
	return ""
}

// publiclyIssued stands in for Let's Encrypt: a root the browser trusts and
// the hive's certificate for its LAN name, issued by it.
func publiclyIssued(t *testing.T, name string) (*x509.CertPool, tls.Certificate) {
	t.Helper()
	now := time.Now()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return roots, tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
}

// browserAtAddress is a browser on the LAN with no working DNS that has
// accepted the hive's certificate at its address after checking the warning
// names the hive: it trusts the roots and the LAN name, never a resolver.
func browserAtAddress(roots *x509.CertPool, lanName string) *http.Client {
	noDNS := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("no DNS in an emergency")
	}}
	dialer := &net.Dialer{Resolver: noDNS, Timeout: 5 * time.Second}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: lanName, MinVersion: tls.VersionTLS12}, DialContext: dialer.DialContext}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

type hiveClient struct {
	base string
	c    *http.Client
}

func tlsClient(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: dial} //nolint:gosec // test certificate
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func (h hiveClient) waitUp(t *testing.T) {
	t.Helper()
	for range 100 {
		if res, err := h.c.Get(h.base + "/healthz"); err == nil {
			_ = res.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the LAN listener never came up")
}

// call sends a JSON request; a JSON array answer comes back under "list".
func (h hiveClient) call(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, h.base+path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := h.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var raw json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&raw)
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '[' {
		var list []any
		_ = json.Unmarshal(raw, &list)
		out["list"] = list
	} else {
		_ = json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out
}
