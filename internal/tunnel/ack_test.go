package tunnel_test

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/tunnel"
)

// ackFixture is a gateway whose OnEvent hook fails while fail is set, and
// a connected hive agent.
func ackFixture(t *testing.T, fail *atomic.Bool, seen *atomic.Int32) *tunnel.Agent {
	t.Helper()
	ca, _ := pki.NewCA("test CA", time.Hour)
	srvTLS, _ := ca.ServerTLSConfig("gateway.test")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	gw := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 200 * time.Millisecond, Hooks: tunnel.Hooks{
		OnEvent: func(pki.HiveID, protocol.Event) error {
			seen.Add(1)
			if fail.Load() {
				return errors.New("the bus is down")
			}
			return nil
		},
	}})
	go func() { _ = gw.Serve(ln) }()
	t.Cleanup(func() { _ = gw.Close() })
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, _ := ca.IssueDevice("hv_ack", pub, time.Hour)
	cliTLS, _ := pki.ClientTLSConfig(ca.Certificate(), cert, priv, "gateway.test")
	ag := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss://" + ln.Addr().String() + "/v1/hive", TLS: cliTLS, HiveID: "hv_ack", Version: "test",
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond}, echoHandler)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ag.Run(ctx)
	for d := time.Now().Add(3 * time.Second); !ag.Connected() && time.Now().Before(d); {
		time.Sleep(10 * time.Millisecond)
	}
	return ag
}

// #172: an event that must arrive is acknowledged by the gateway once its
// hook took it (the cloud published it); until then EmitAcked fails and
// the hive sends it again.
func TestAnAcknowledgedEventReturnsOnlyOnceTheGatewayTookIt(t *testing.T) {
	var fail atomic.Bool
	var seen atomic.Int32
	ag := ackFixture(t, &fail, &seen)
	e := protocol.Event{Name: "recording.stopped", At: time.Now()}

	fail.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	err := ag.EmitAcked(ctx, e)
	cancel()
	if err == nil || seen.Load() != 1 {
		t.Fatalf("EmitAcked = %v with the hook failing (seen %d), want an error after the gateway saw it once", err, seen.Load())
	}

	fail.Store(false)
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := ag.EmitAcked(ctx, e); err != nil {
		t.Fatalf("EmitAcked = %v, want acknowledged", err)
	}
}

func TestEmitAckedWhileDisconnectedFails(t *testing.T) {
	ag := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss://127.0.0.1:1/v1/hive", HiveID: "hv_x"}, echoHandler)
	if err := ag.EmitAcked(context.Background(), protocol.Event{Name: "recording.stopped"}); !errors.Is(err, tunnel.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

// #172 review: a gateway from before the ack (its welcome does not
// announce acks) gets the event once, without an id, and EmitAcked
// returns at once: no resend loop against an old cloud.
func TestAnOldGatewayGetsAPlainEventAndNoWait(t *testing.T) {
	got := make(chan protocol.Envelope, 4)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ctx := r.Context()
		if _, _, err := ws.Read(ctx); err != nil { // hello
			return
		}
		welcome, _ := protocol.NewEnvelope(protocol.KindWelcome, "", map[string]any{"server_time": time.Now(), "ping_interval": int64(time.Minute), "min_protocol_version": protocol.Version})
		raw, _ := protocol.Encode(welcome)
		if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
			return
		}
		for {
			_, msg, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if env, err := protocol.Decode(msg); err == nil {
				got <- env
			}
		}
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	ag := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss" + strings.TrimPrefix(srv.URL, "https") + "/v1/hive", TLS: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		HiveID: "hv_old", Version: "test", ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond}, echoHandler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	for d := time.Now().Add(3 * time.Second); !ag.Connected() && time.Now().Before(d); {
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	ectx, ecancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ecancel()
	if err := ag.EmitAcked(ectx, protocol.Event{Name: "recording.stopped", At: time.Now()}); err != nil {
		t.Fatalf("EmitAcked = %v, want nil against an old gateway", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("EmitAcked waited for an ack an old gateway never sends")
	}
	select {
	case env := <-got:
		if env.Kind != protocol.KindEvent || env.ID != "" {
			t.Fatalf("envelope = %+v, want a plain event without an id", env)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the old gateway got nothing")
	}
}
