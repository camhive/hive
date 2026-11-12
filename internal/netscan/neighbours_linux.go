package netscan

import (
	"context"
	"net/netip"
	"os"
)

func neighbours(context.Context) (map[netip.Addr]string, error) {
	raw, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	return parseProcARP(string(raw)), nil
}
