package netscan

import (
	"context"
	"net/netip"
	"os/exec"
)

func neighbours(ctx context.Context) (map[netip.Addr]string, error) {
	raw, err := exec.CommandContext(ctx, "arp", "-a").Output()
	if err != nil {
		return nil, err
	}
	return parseArpA(string(raw)), nil
}
