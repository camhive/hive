package hiveagent

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
)

// Relay is what tunnel.open needs: the device identity to dial the cloud
// relay back over mTLS and a way into the hive API's relay listener to pipe
// the client's bytes into (ADR-0010 path 4).
type Relay struct {
	CA         *x509.Certificate
	Cert       *x509.Certificate
	Key        crypto.PrivateKey
	AllowHost  string        // relay host the cloud may name (empty: any wss host)
	ServerName string        // expected name in the relay certificate (dev relays reached by IP); empty: the URL host
	Idle       time.Duration // stream idle timeout; default 5 min
	// Dial opens a connection to the hive API's relay listener, so the API
	// can tell relayed (remote) requests from LAN ones (#58).
	Dial func(ctx context.Context) (net.Conn, error)
}

// dialRelay opens the WebSocket back to the relay with the device identity.
func (r *Relay) dialRelay(ctx context.Context, u *url.URL) (*websocket.Conn, error) {
	serverName := r.ServerName
	if serverName == "" {
		serverName = u.Hostname()
	}
	tlsCfg, err := pki.ClientTLSConfig(r.CA, r.Cert, r.Key, serverName)
	if err != nil {
		return nil, err
	}
	ws, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}}, CompressionMode: websocket.CompressionDisabled})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("relay dial: %w", err)
	}
	return ws, nil
}

func (r *Relay) idle() time.Duration {
	if r.Idle == 0 {
		return 5 * time.Minute
	}
	return r.Idle
}

// SetRelay enables tunnel.open.
func (a *Agent) SetRelay(r Relay) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.relay = &r
}

// tunnelOpen dials the relay for one stream and pipes it to the local TLS
// API. It answers as soon as the dial succeeds; the pipe runs on its own.
func (a *Agent) tunnelOpen(ctx context.Context, args json.RawMessage) protocol.Result {
	a.mu.Lock()
	r := a.relay
	a.mu.Unlock()
	if r == nil || r.Dial == nil {
		return protocol.Fail(protocol.ErrCodeNotReady, "relay not configured")
	}
	in, u, res := parseTunnelOpen(args, r)
	if res != nil {
		return *res
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, err := r.dialRelay(dialCtx, u)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	local, err := r.Dial(dialCtx)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "local api")
		return protocol.Fail(protocol.ErrCodeNotReady, "local API not listening: "+err.Error())
	}
	go a.pipeStream(context.WithoutCancel(ctx), ws, local, r.idle())
	a.record(ctx, "tunnel.open", in.StreamID, map[string]any{"relay": u.Host})
	return protocol.Succeed(nil)
}

type tunnelOpenArgs struct {
	StreamID string `json:"stream_id"`
	URL      string `json:"url"`
}

// parseTunnelOpen validates the command: a wss URL ending in the stream id
// on the allowed relay host.
func parseTunnelOpen(args json.RawMessage, r *Relay) (tunnelOpenArgs, *url.URL, *protocol.Result) {
	var in tunnelOpenArgs
	if res := decodeArgs(args, &in); res != nil {
		return in, nil, res
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "wss" || in.StreamID == "" || !strings.HasSuffix(u.Path, "/"+in.StreamID) {
		res := protocol.Fail(protocol.ErrCodeInvalid, "stream_id and a wss:// url ending in the stream id are required")
		return in, nil, &res
	}
	if r.AllowHost != "" && !strings.EqualFold(u.Hostname(), r.AllowHost) {
		res := protocol.Fail(protocol.ErrCodeInvalid, "relay host not allowed")
		return in, nil, &res
	}
	return in, u, nil
}

// pipeStream copies relay ↔ local API until either side ends or idles.
func (a *Agent) pipeStream(ctx context.Context, ws *websocket.Conn, local net.Conn, idle time.Duration) {
	remote := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	done := make(chan struct{}, 2)
	copyDir := func(dst io.Writer, src net.Conn, closeDst func()) {
		buf := make([]byte, 32<<10)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		closeDst()
		done <- struct{}{}
	}
	go copyDir(local, remote, func() {
		if tc, ok := local.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	})
	go copyDir(remote, local, func() { _ = remote.Close() })
	<-done
	<-done
	_ = local.Close()
	_ = ws.Close(websocket.StatusNormalClosure, "")
}
