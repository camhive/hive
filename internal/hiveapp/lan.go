package hiveapp

import (
	"context"
	"net"
	"net/http"
)

// The LAN listener is the TLS listener on the hive's network interfaces
// (ADR-0012). The local emergency login (#57) and its tokens are accepted
// only there, only from a private or link-local address, and never through
// the relay: an attacker on the internet has no way to try them. Loopback is
// refused too: on the TLS listener it means a forwarder on the hive machine
// (ssh -R, a tunnel agent, a reverse proxy) that may carry outside traffic,
// and the app always uses the LAN name. A proxy or a router that rewrites
// the source of forwarded traffic to a private address defeats the check;
// docs/security/threat-model.md says not to expose the port that way.

type lanKey struct{}

// LANConn is an http.Server ConnContext that marks every request on the
// connection as arriving on the LAN listener.
func LANConn(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, lanKey{}, true)
}

// onLAN reports whether the request came to the LAN listener from another
// machine inside a local network.
func onLAN(r *http.Request) bool {
	marked, _ := r.Context().Value(lanKey{}).(bool)
	if !marked || Relayed(r.Context()) {
		return false
	}
	ip := net.ParseIP(remoteHost(r))
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
