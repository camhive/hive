package hiveapp

import (
	"context"
	"net"
	"time"
)

// Requests that reach the hive through the cloud relay are remote; requests on
// the LAN listener are not. The plan limits remote use only (#58): recording,
// LAN viewing and local export never depend on the plan. The relay therefore
// does not dial the TLS port like a LAN client would; it hands its streams to
// a RelayListener, whose server marks every connection with RelayedConn.

type relayedKey struct{}

// RelayedConn is an http.Server ConnContext that marks every request on the
// connection as relayed.
func RelayedConn(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, relayedKey{}, true)
}

// Relayed reports whether the request came through the cloud relay.
func Relayed(ctx context.Context) bool {
	v, _ := ctx.Value(relayedKey{}).(bool)
	return v
}

// RelayListener is a loopback listener on a port of its own that only the
// agent's tunnel dials; the API's relay server accepts from it. A local
// process that connects here gets nothing but the remote limits.
type RelayListener struct {
	net.Listener
}

// NewRelayListener listens on a random loopback port.
func NewRelayListener(ctx context.Context) (*RelayListener, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &RelayListener{Listener: ln}, nil
}

// Dial connects to the listener.
func (l *RelayListener) Dial(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", l.Addr().String())
}
