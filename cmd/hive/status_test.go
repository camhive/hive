package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kardianos/service"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/recording"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/version"
)

// runningHive serves the hive API of a hive at running that hive-updater
// updated from 0.1.5 and that has 0.1.7 on offer, on the listen_addr of
// the hive.yaml in the returned paths.
func runningHive(t *testing.T, running string) hiveconfig.Paths {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	at := time.Date(2026, 11, 16, 3, 12, 0, 0, time.UTC)
	for _, a := range []store.Action{
		{At: at, Actor: "updater", Action: "update.applied", Target: "hive", Details: []byte(`{"from":"0.1.5","to":"0.1.6"}`)},
		{At: at.Add(time.Hour), Actor: "updater", Action: "update.available", Target: "hive", Details: []byte(`{"offered":"0.1.7"}`)},
	} {
		if err := st.Actions().Append(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Store: st, Version: running}))
	t.Cleanup(srv.Close)
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if err := os.WriteFile(paths.Config, []byte("listen_addr: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths
}

// #109: after hive-updater installed 0.1.6, `hive status` run from the
// 0.1.5 package says what runs, what the updater did and offers, and
// that this command is the older one.
func TestStatusReportsTheRunningHiveNotThePackageBinary(t *testing.T) {
	paths := runningHive(t, "0.1.6 (a73f337, 2026-11-15)")
	var out bytes.Buffer
	if code := runStatus(running, version.Info{Version: "0.1.5", Commit: "0af03ce"}, paths, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, want := range []string{
		"running\n",
		"hive: 0.1.6 (a73f337, 2026-11-15)\n",
		"last update: 0.1.5 -> 0.1.6, applied " + time.Date(2026, 11, 16, 3, 12, 0, 0, time.UTC).Local().Format("2006-01-02 15:04 -07:00") + "\n",
		"update on offer: 0.1.7\n",
		"this command: 0.1.5 (0af03ce), older than the running hive; hive-updater runs its own copy\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "running\n") {
		t.Errorf("the service state stays the first line:\n%s", got)
	}
}

func TestStatusSaysNothingAboutTheCommandWhenItIsCurrent(t *testing.T) {
	paths := runningHive(t, "0.1.6 (a73f337, 2026-11-15)")
	for _, self := range []version.Info{{Version: "0.1.6"}, {Version: "0.1.7"}, {}} { // {} is a dev build
		var out bytes.Buffer
		runStatus(running, self, paths, &out, io.Discard)
		if strings.Contains(out.String(), "this command") {
			t.Errorf("%s: %s", self, out.String())
		}
	}
}

func TestStatusSaysWhenNoHiveAnswers(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	_ = os.WriteFile(paths.Config, []byte("listen_addr: 127.0.0.1:1\n"), 0o600)
	var out bytes.Buffer
	runStatus(func() (service.Status, error) { return service.StatusStopped, nil }, version.Info{Version: "0.1.5"}, paths, &out, io.Discard)
	if got := out.String(); got != "stopped\nhive: not answering on 127.0.0.1:1\n" {
		t.Fatalf("got %q", got)
	}
}

// A hive in the foreground (`hive run`) has no service but still answers.
func TestStatusWithoutAServiceStillAsksTheHive(t *testing.T) {
	paths := runningHive(t, "0.1.6")
	var out, errb bytes.Buffer
	code := runStatus(func() (service.Status, error) { return service.StatusUnknown, service.ErrNotInstalled }, version.Info{}, paths, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "not installed") || out.String() == "" || !strings.HasPrefix(out.String(), "hive: 0.1.6\n") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func running() (service.Status, error) { return service.StatusRunning, nil }

// A user who cannot read hive.yaml (0600, root) still reaches a hive on
// the default address.
// It says so on stderr, so "not answering" or a wrong answer makes sense.
func TestHiveAddressFallsBackToTheDefault(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir()) // no hive.yaml
	var errb bytes.Buffer
	if got := hiveAddr(paths, &errb); got != hiveconfig.DefaultListenAddr {
		t.Fatalf("addr = %q", got)
	}
	if want := "hive.yaml not readable, tried the default " + hiveconfig.DefaultListenAddr; !strings.Contains(errb.String(), want) {
		t.Fatalf("stderr %q, want %q", errb.String(), want)
	}
	errb.Reset()
	if hiveAddr(runningHive(t, "0.1.6"), &errb); errb.Len() != 0 {
		t.Fatalf("a readable hive.yaml needs no hint: %q", errb.String())
	}
}

// `hive version` still prints its own version, marked as the package
// binary, and then what runs.
func TestVersionMarksThePackageBinaryAndNamesTheRunningHive(t *testing.T) {
	paths := runningHive(t, "0.1.6 (a73f337, 2026-11-15)")
	var out bytes.Buffer
	reportVersion(context.Background(), &out, io.Discard, version.Info{Version: "0.1.5", Commit: "0af03ce"}, paths)
	want := "camhive-hive 0.1.5 (0af03ce) (this binary)\n" +
		"running hive: 0.1.6 (a73f337, 2026-11-15)\n" +
		"this command: 0.1.5 (0af03ce), older than the running hive; hive-updater runs its own copy\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}

	none := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	_ = os.WriteFile(none.Config, []byte("listen_addr: 127.0.0.1:1\n"), 0o600)
	out.Reset()
	reportVersion(context.Background(), &out, io.Discard, version.Info{Version: "0.1.5"}, none)
	if out.String() != "camhive-hive 0.1.5 (this binary)\n" {
		t.Fatalf("no hive: %q", out.String())
	}
}

func TestOlderComparesReleasesOnly(t *testing.T) {
	for _, c := range []struct {
		self    string
		running string
		want    bool
	}{
		{"0.1.5", "0.1.6 (a73f337, 2026-11-15)", true},
		{"0.1.6", "0.1.6", false},
		{"0.1.7", "0.1.6", false},
		{"", "0.1.6", false},    // a dev build has no release to compare
		{"0.1.5", "dev", false}, // nor has a dev hive
		{"0.1.5", "", false},
	} {
		if got := older(version.Info{Version: c.self}, c.running); got != c.want {
			t.Errorf("older(%q, %q) = %v", c.self, c.running, got)
		}
	}
}

// The commands that need no service are dispatched without one: `hive
// version` among them (#109), `hive --version` stays one line in run.
func TestStandaloneDispatchesVersionWithoutTheService(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	_ = os.WriteFile(paths.Config, []byte("listen_addr: 127.0.0.1:1\n"), 0o600)
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if code, ok := standalone("version", []string{"version"}, paths, out, out); !ok || code != 0 {
		t.Fatalf("version: %d %v", code, ok)
	}
	if code, ok := standalone("local-login", []string{"local-login"}, paths, out, out); !ok || code != 2 {
		t.Fatalf("local-login without arguments: %d %v", code, ok)
	}
	if _, ok := standalone("start", []string{"start"}, paths, out, out); ok {
		t.Fatal("start needs the service")
	}
	raw, _ := os.ReadFile(out.Name())
	if got := string(raw); !strings.HasPrefix(got, "camhive-hive ") || !strings.Contains(got, "(this binary)\n") || !strings.Contains(got, "usage: hive local-login") {
		t.Fatalf("output %q", got)
	}
}

// ADR-0023: when the hive's address changed during an outage, the owner
// reads the new one on the hive's computer and types it into the app.
func TestLANLinesNameEveryAddressWithTheTLSPort(t *testing.T) {
	got := lanLines([]string{"192.168.1.23", "fd00::5"}, "0.0.0.0:16443")
	want := []string{"lan: https://192.168.1.23:16443", "lan: https://[fd00::5]:16443"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lanLines = %q", got)
	}
	if got := lanLines([]string{"192.168.1.23"}, ""); got != nil {
		t.Fatalf("no TLS listener, no LAN lines: %q", got)
	}
}

func TestStatusPrintsTheHivesLANAddresses(t *testing.T) {
	paths := runningHive(t, "0.1.6 (a73f337, 2026-11-15)")
	var out bytes.Buffer
	runStatus(running, version.Info{Version: "0.1.6"}, paths, &out, io.Discard)
	addrs := hiveagent.LANAddrs()
	for _, a := range addrs {
		if want := "lan: https://" + net.JoinHostPort(a, "16443") + "\n"; !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	if len(addrs) == 0 && strings.Contains(out.String(), "lan: ") {
		t.Errorf("no private address, yet a LAN line:\n%s", out.String())
	}
}

// #172: `hive status` says the running hive does not record, since when
// and why, right after the version.
func TestStatusSaysTheHiveDoesNotRecord(t *testing.T) {
	since := time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Version: "0.1.11", Recording: func() recording.State {
		return recording.State{Since: since, Reason: recording.ArchiveMissing, Detail: "stat /media/disk/camhive/archive: no such file or directory"}
	}}))
	t.Cleanup(srv.Close)
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if err := os.WriteFile(paths.Config, []byte("listen_addr: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runStatus(running, version.Info{Version: "0.1.11"}, paths, &out, io.Discard)
	want := "hive: 0.1.11\nNOT RECORDING since " + since.Local().Format("2006-01-02 15:04 -07:00") +
		": archive_missing (stat /media/disk/camhive/archive: no such file or directory)\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("status =\n%s\nwant it to contain\n%s", out.String(), want)
	}
}
