//go:build !linux && !windows

package netscan

import (
	"context"
	"errors"
	"net/netip"
)

func neighbours(context.Context) (map[netip.Addr]string, error) {
	return nil, errors.New("netscan: neighbour table not supported on this platform")
}
