package hivelink_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivelink"
	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/tunnel"
)

// fakeCloud is the API as the hive sees it, plus a certificate issuer.
type fakeCloud struct {
	ca         *pki.CA
	gatewayURL string
	certTTL    time.Duration

	mu      sync.Mutex
	pending map[string]ed25519.PublicKey // code -> key
	issued  map[string]string            // code -> hive id
	renews  atomic.Int32
}

func (f *fakeCloud) issue(id string, pub ed25519.PublicKey) (certPEM, caPEM []byte) {
	cert, _ := f.ca.IssueDevice(pki.HiveID(id), pub, f.certTTL)
	caPEM, _, _ = f.ca.PEM()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), caPEM
}

func (f *fakeCloud) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/claim/codes", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PublicKey []byte `json:"public_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.pending["ABCD-2345"] = in.PublicKey
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "ABCD-2345", "expires_at": time.Now().Add(10 * time.Minute)})
	})
	mux.HandleFunc("GET /v1/claim/codes/{code}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		code := r.PathValue("code")
		if id, ok := f.issued[code]; ok {
			certPEM, caPEM := f.issue(id, f.pending[code])
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "issued", "hive_id": id, "cert_pem": string(certPEM), "ca_pem": string(caPEM), "gateway_url": f.gatewayURL})
			return
		}
		if _, ok := f.pending[code]; ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unknown_code", "message": "gone"}})
	})
	mux.HandleFunc("POST /v1/claim/renew", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			HiveID    string    `json:"hive_id"`
			At        time.Time `json:"at"`
			Signature []byte    `json:"signature"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		pub := f.pending["ABCD-2345"]
		f.mu.Unlock()
		if !ed25519.Verify(pub, hivelink.RenewMessage(in.HiveID, in.At), in.Signature) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.renews.Add(1)
		certPEM, caPEM := f.issue(in.HiveID, pub)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "issued", "cert_pem": string(certPEM), "ca_pem": string(caPEM)})
	})
	return mux
}

func TestClaimThenRunConnectsAndRenewsOnSchedule(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Hour)
	cloud := &fakeCloud{ca: ca, certTTL: 3 * time.Second, pending: map[string]ed25519.PublicKey{}, issued: map[string]string{}}
	api := httptest.NewServer(cloud.handler())
	defer api.Close()

	// A bare tunnel gateway (no NATS) with the renewal handler mounted.
	srvTLS, _ := ca.ServerTLSConfig("gw.test")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	gw := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 200 * time.Millisecond})
	go func() { _ = gw.Serve(ln) }()
	defer func() { _ = gw.Close() }()
	cloud.gatewayURL = "wss://" + ln.Addr().String()

	paths := hiveconfig.DefaultPaths().WithDataDir(t.TempDir())
	key, _, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hivelink.Load(paths); !errors.Is(err, hivelink.ErrNotClaimed) {
		t.Fatalf("unclaimed load: %v", err)
	}
	if err := hivelink.Run(context.Background(), paths, key, hivelink.RunOptions{}); !errors.Is(err, hivelink.ErrNotClaimed) {
		t.Fatalf("run unclaimed: %v", err)
	}

	// Owner claims after the first poll.
	go func() {
		time.Sleep(150 * time.Millisecond)
		cloud.mu.Lock()
		cloud.issued["ABCD-2345"] = "hv_0123456789abcdef"
		cloud.mu.Unlock()
	}()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link, err := hivelink.Claim(ctx, paths, key, hivelink.ClaimOptions{API: api.URL, Interval: 50 * time.Millisecond, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if link.HiveID != "hv_0123456789abcdef" || link.GatewayURL != cloud.gatewayURL || !strings.Contains(out.String(), "ABCD-2345") {
		t.Fatalf("link = %+v out=%q", link, out.String())
	}
	if _, err := hivelink.Claim(ctx, paths, key, hivelink.ClaimOptions{API: api.URL}); !errors.Is(err, hivelink.ErrAlreadyLinked) {
		t.Fatalf("second claim: %v", err)
	}
	st, err := hivelink.Load(paths)
	if err != nil || st.Cert.NotAfter.Sub(st.Cert.NotBefore) <= 0 || pki.HiveIDFromCertificate(st.Cert) != pki.HiveID(link.HiveID) {
		t.Fatalf("state = %+v err=%v", st, err)
	}
	firstSerial := st.Cert.SerialNumber.String()
	// The test gateway listens on an IP; tell the hive which name to expect.
	st.Link.ServerName = "gw.test"
	certPEM, _ := os.ReadFile(paths.Cert)
	caPEM, _ := os.ReadFile(paths.CACert)
	if err := hivelink.Save(paths, st.Link, certPEM, caPEM); err != nil {
		t.Fatal(err)
	}

	// Run: connects; the 3s certificate is renewed at ~2s and the tunnel
	// comes back with the new one.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	done := make(chan error, 1)
	go func() {
		done <- hivelink.Run(runCtx, paths, key, hivelink.RunOptions{Version: "t", RenewCheck: 100 * time.Millisecond, ReconnectMin: 50 * time.Millisecond, ReconnectMax: 200 * time.Millisecond})
	}()
	waitFor(t, 5*time.Second, func() bool { return gw.Online(pki.HiveID(link.HiveID)) }, "online")
	waitFor(t, 10*time.Second, func() bool { return cloud.renews.Load() >= 1 }, "renewal")
	waitFor(t, 5*time.Second, func() bool {
		st, err := hivelink.Load(paths)
		return err == nil && st.Cert.SerialNumber.String() != firstSerial && gw.Online(pki.HiveID(link.HiveID))
	}, "reconnected with the renewed certificate")
	stopRun()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestClaimReportsExpiredCode(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "GONE-GONE", "expires_at": time.Now()})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	paths := hiveconfig.DefaultPaths().WithDataDir(t.TempDir())
	key, _, _ := hiveconfig.LoadOrCreateKey(paths)
	_, err := hivelink.Claim(context.Background(), paths, key, hivelink.ClaimOptions{API: api.URL, Interval: 10 * time.Millisecond, Out: &bytes.Buffer{}})
	if !errors.Is(err, hivelink.ErrCodeExpired) {
		t.Fatalf("err = %v", err)
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}
