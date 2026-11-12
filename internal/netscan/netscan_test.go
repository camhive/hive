package netscan

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestParseProcARPKeepsCompleteEntries(t *testing.T) {
	raw := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.10     0x1         0x2         AA:bb:cc:00:11:22     *        eth0
192.168.1.11     0x1         0x0         00:00:00:00:00:00     *        eth0
10.0.0.5         0x1         0x2         de:ad:be:ef:00:01     *        wlan0
`
	got := parseProcARP(raw)
	if len(got) != 2 || got[netip.MustParseAddr("192.168.1.10")] != "aa:bb:cc:00:11:22" || got[netip.MustParseAddr("10.0.0.5")] != "de:ad:be:ef:00:01" {
		t.Fatalf("got %v", got)
	}
}

func TestParseArpAOfWindows(t *testing.T) {
	raw := "\r\nInterface: 192.168.1.2 --- 0x7\r\n  Internet Address      Physical Address      Type\r\n" +
		"  192.168.1.1           aa-bb-cc-00-11-22     dynamic   \r\n" +
		"  192.168.1.255         ff-ff-ff-ff-ff-ff     static    \r\n" +
		"  224.0.0.22            01-00-5e-00-00-16     static    \r\n" +
		"  192.168.1.7           aa-bb-cc-00-11-77     dynamisch \r\n" // a German Windows
	got := parseArpA(raw)
	if len(got) != 2 || got[netip.MustParseAddr("192.168.1.1")] != "aa:bb:cc:00:11:22" || got[netip.MustParseAddr("192.168.1.7")] != "aa:bb:cc:00:11:77" {
		t.Fatalf("got %v", got)
	}
}

// ADR-0017: a sweep never covers more than the /24 around the hive.
func TestSearchablePrefixIsAtMostA24AroundTheHive(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20/24": "192.168.1.0/24",
		"10.1.2.3/16":     "10.1.2.0/24",
		"192.168.1.20/28": "192.168.1.16/28",
	} {
		if got := searchable(netip.MustParsePrefix(in)); got.String() != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
	hosts := hostsOf(netip.MustParsePrefix("192.168.1.16/28"))
	if len(hosts) != 14 || hosts[0].String() != "192.168.1.17" || hosts[13].String() != "192.168.1.30" {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestOnlyUnicastIPv4NetworksOfTheHiveAreSearched(t *testing.T) {
	cidr := func(s string) net.Addr {
		ip, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	got := searchablePrefixes([]net.Addr{
		cidr("127.0.0.1/8"), cidr("169.254.3.4/16"), cidr("fe80::1/64"), cidr("2001:db8::5/64"),
		cidr("192.168.1.20/24"), cidr("10.1.2.3/16"), &net.IPAddr{IP: net.ParseIP("192.168.5.5")},
	})
	if len(got) != 2 || got[0].String() != "192.168.1.0/24" || got[1].String() != "10.1.2.0/24" {
		t.Fatalf("got %v", got)
	}
	if _, err := Subnets(); err != nil {
		t.Fatal(err)
	}
}

func TestSweepTouchesEveryHostAndStopsWhenCancelled(t *testing.T) {
	if err := Sweep(context.Background(), netip.MustParsePrefix("127.0.0.0/30")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sweep(ctx, netip.MustParsePrefix("127.0.0.0/24")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
