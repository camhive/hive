// Package netscan finds cameras on the Hive's own networks again after
// their address changed (ADR-0017): the neighbour table that maps IP to MAC,
// the Hive's subnets, a gentle sweep that makes the operating system learn
// every neighbour, and a credential check that never sends a password in
// the clear.
package netscan

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Neighbours returns the operating system's IPv4 neighbour table: IP → MAC
// (lower case, colon separated), complete entries only.
func Neighbours(ctx context.Context) (map[netip.Addr]string, error) { return neighbours(ctx) }

// Subnets lists the searchable prefixes of the Hive's up, non-loopback IPv4
// interfaces: each at most the /24 around the Hive's address.
func Subnets() ([]netip.Prefix, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	return searchablePrefixes(addrs), nil
}

// searchablePrefixes keeps the unicast, non-loopback, non-link-local IPv4
// networks among interface addresses, each narrowed to its /24.
func searchablePrefixes(addrs []net.Addr) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok || !ip.Unmap().Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		ones, _ := n.Mask.Size()
		out = append(out, searchable(netip.PrefixFrom(ip.Unmap(), ones)))
	}
	return out
}

// searchable narrows a prefix to at most the /24 around its address.
func searchable(p netip.Prefix) netip.Prefix {
	if p.Bits() < 24 {
		p = netip.PrefixFrom(p.Addr(), 24)
	}
	return p.Masked()
}

// hostsOf lists a prefix's host addresses (without network and broadcast).
func hostsOf(p netip.Prefix) []netip.Addr {
	var out []netip.Addr
	first := p.Masked().Addr()
	for a := first.Next(); p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) { // broadcast
			break
		}
		out = append(out, a)
	}
	return out
}

// SweepRate is how many addresses a sweep touches per second.
const SweepRate = 50

// Sweep sends one empty UDP datagram (to the discard port) to every host of
// the prefix, paced at SweepRate, so the operating system resolves each
// neighbour's MAC. Nothing listens for answers; then read Neighbours.
func Sweep(ctx context.Context, p netip.Prefix) error {
	t := time.NewTicker(time.Second / SweepRate)
	defer t.Stop()
	for _, a := range hostsOf(p) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, 9))); err == nil {
			_, _ = c.Write(nil)
			_ = c.Close()
		}
	}
	return nil
}

// parseProcARP reads Linux /proc/net/arp; flags 0x0 are incomplete entries.
func parseProcARP(raw string) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[2] == "0x0" {
			continue
		}
		if ip, err := netip.ParseAddr(f[0]); err == nil {
			addMAC(out, ip, f[3])
		}
	}
	return out
}

// parseArpA reads `arp -a` on Windows. The type column is translated
// ("dynamic", "dynamisch", …), so entries are kept by their addresses:
// unicast IPv4 with a unicast MAC.
func parseArpA(raw string) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if ip, err := netip.ParseAddr(f[0]); err == nil && ip.Is4() && !ip.IsMulticast() {
			addMAC(out, ip, strings.ReplaceAll(f[1], "-", ":"))
		}
	}
	return out
}

func addMAC(out map[netip.Addr]string, ip netip.Addr, mac string) {
	mac = strings.ToLower(mac)
	if hw, err := net.ParseMAC(mac); err == nil && mac != "00:00:00:00:00:00" && hw[0]&1 == 0 { // no multicast or broadcast
		out[ip] = mac
	}
}
