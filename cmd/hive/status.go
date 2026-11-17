package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kardianos/service"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/version"
)

// hive-updater runs its own copy of the hive (ADR-0018), so this command
// may be an older binary than the hive it reports on (#109). It asks the
// running hive over the loopback /healthz that the updater checks it with.

// runStatus prints the service state, then what the running hive says. A
// hive in the foreground (`hive run`) answers without a service; the
// exit code still says the service could not be read.
func runStatus(status func() (service.Status, error), self version.Info, paths hiveconfig.Paths, stdout, stderr io.Writer) int {
	code := 0
	if st, err := status(); err != nil {
		fmt.Fprintln(stderr, err)
		code = 1
	} else {
		fmt.Fprintln(stdout, statusString(st))
	}
	reportHive(context.Background(), stdout, stderr, self, paths)
	return code
}

// reportHive prints what the running hive says about itself.
func reportHive(ctx context.Context, w, stderr io.Writer, self version.Info, paths hiveconfig.Paths) {
	cfg := hiveConfig(paths, stderr)
	h, err := askHive(ctx, cfg.ListenAddr)
	if err != nil {
		fmt.Fprintln(w, "hive: not answering on", cfg.ListenAddr)
		return
	}
	fmt.Fprintln(w, "hive:", h.Version)
	if u := h.Updates; u != nil {
		printUpdates(w, *u)
	}
	printOlder(w, self, h.Version)
	for _, l := range lanLines(hiveagent.LANAddrs(), cfg.TLSListenAddr) {
		fmt.Fprintln(w, l)
	}
}

// lanLines are the addresses a browser on the LAN opens the hive at
// (ADR-0023), one per private address, with the TLS listener's port; none
// without a TLS listener.
func lanLines(addrs []string, tlsListen string) []string {
	_, port, err := net.SplitHostPort(tlsListen)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		out = append(out, "lan: https://"+net.JoinHostPort(a, port))
	}
	return out
}

// reportVersion prints this binary's version, which may be older than
// what runs, and the running hive's when one answers.
func reportVersion(ctx context.Context, w, stderr io.Writer, self version.Info, paths hiveconfig.Paths) {
	fmt.Fprintln(w, "camhive-hive", self, "(this binary)")
	h, err := askHive(ctx, hiveAddr(paths, stderr))
	if err != nil {
		return
	}
	fmt.Fprintln(w, "running hive:", h.Version)
	printOlder(w, self, h.Version)
}

func printUpdates(w io.Writer, u hiveapp.UpdateReports) {
	if l := u.Last; l != nil {
		fmt.Fprintf(w, "last update: %s -> %s, %s %s\n", l.From, l.To, strings.TrimPrefix(l.Action, "update."), l.At.Local().Format("2006-01-02 15:04 -07:00"))
	}
	if u.Available != "" {
		fmt.Fprintln(w, "update on offer:", u.Available)
	}
	if u.NeedsPackage != "" {
		fmt.Fprintln(w, "needs a package install:", u.NeedsPackage)
	}
}

func printOlder(w io.Writer, self version.Info, running string) {
	if older(self, running) {
		fmt.Fprintf(w, "this command: %s, older than the running hive; hive-updater runs its own copy\n", self)
	}
}

// hiveAddr is the hive's loopback API address from hive.yaml, or the
// default for a user who cannot read it.
func hiveAddr(paths hiveconfig.Paths, stderr io.Writer) string {
	return hiveConfig(paths, stderr).ListenAddr
}

// hiveConfig is hive.yaml, or the default listeners for a user who cannot
// read it (it is 0600); stderr says so.
func hiveConfig(paths hiveconfig.Paths, stderr io.Writer) hiveconfig.Config {
	cfg, err := hiveconfig.Read(paths)
	if err != nil {
		fmt.Fprintln(stderr, "hive.yaml not readable, tried the default", hiveconfig.DefaultListenAddr, "(run as root / Administrator to use its listen_addr)")
		return hiveconfig.Config{ListenAddr: hiveconfig.DefaultListenAddr, TLSListenAddr: hiveconfig.DefaultTLSListenAddr}
	}
	return cfg
}

// askHive asks the hive at addr for its /healthz. addr is listen_addr,
// which hiveconfig accepts only as a loopback address, or its default.
func askHive(ctx context.Context, addr string) (hiveapp.Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil) //nolint:gosec // loopback listen_addr, see above
	if err != nil {
		return hiveapp.Health{}, err
	}
	res, err := http.DefaultClient.Do(req) //nolint:gosec // loopback listen_addr, see above
	if err != nil {
		return hiveapp.Health{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var h hiveapp.Health
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&h); err != nil || h.Status != "ok" {
		return hiveapp.Health{}, errors.New("no hive answers there")
	}
	return h, nil
}

// older reports whether self is an older release than running, which a
// hive prints as "1.2.3 (commit, date)". Dev builds compare as neither.
func older(self version.Info, running string) bool {
	mine, err := update.ParseVersion(self.Version)
	if err != nil {
		return false
	}
	theirs, err := update.ParseVersion(strings.TrimSpace(strings.SplitN(running, " ", 2)[0]))
	return err == nil && mine.Compare(theirs) < 0
}
