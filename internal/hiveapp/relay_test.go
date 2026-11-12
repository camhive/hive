package hiveapp_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveapp"
)

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hive"}, DNSNames: []string{"hive"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// get sends GET /where over an established connection and returns the body.
func get(t *testing.T, c net.Conn) string {
	t.Helper()
	tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: "hive"}) //nolint:gosec // test certificate
	defer func() { _ = tc.Close() }()
	if _, err := io.WriteString(tc, "GET /where HTTP/1.1\r\nHost: hive\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// #58: streams the relay hands to the RelayListener reach the API marked as
// relayed; LAN clients on the TLS port do not.
func TestRelayListenerMarksRequestsAsRelayed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsAddr := ln.Addr().String()
	_ = ln.Close()
	relay, err := hiveapp.NewRelayListener(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hiveapp.Relayed(r.Context()) {
			_, _ = io.WriteString(w, "relay")
			return
		}
		_, _ = io.WriteString(w, "lan")
	})
	app := hiveapp.New(hiveapp.Config{ListenAddr: "127.0.0.1:0", TLSListenAddr: tlsAddr, TLS: &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, MinVersion: tls.VersionTLS12}, Relay: relay, Handler: h})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() { cancel(); <-done }()

	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	rc, err := relay.Dial(dctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, rc); got != "relay" {
		t.Fatalf("relayed request seen as %q", got)
	}
	var lc net.Conn
	for range 100 {
		if lc, err = net.Dial("tcp", tlsAddr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, lc); got != "lan" {
		t.Fatalf("LAN request seen as %q", got)
	}

	_ = relay.Close()
	if _, err := relay.Dial(context.Background()); err == nil {
		t.Fatal("dial after close succeeded")
	}
}
