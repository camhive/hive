package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/datamove"
	"github.com/camhive/hive/internal/hiveconfig"
)

func TestTheDataDirIsReadFromTheUnitAndItsDropIn(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "camhive-hive.service")
	body := "[Service]\nExecStart=/usr/bin/hive-updater \"run\"\nEnvironment=CAMHIVE_DATA_DIR=/media/usb/camhive\nRestart=always\n"
	if err := os.WriteFile(unit, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unitDataDir(unit); got != "/media/usb/camhive" {
		t.Fatalf("data dir = %q", got)
	}
	if err := os.MkdirAll(unit+".d", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unit+".d", dropInName), []byte(dropInFor("/mnt/new/camhive")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unitDataDir(unit); got != "/mnt/new/camhive" {
		t.Fatalf("data dir with the move's drop-in = %q, want the drop-in's", got)
	}
	if got := unitDataDir(filepath.Join(dir, "none")); got != "" {
		t.Fatalf("data dir without a unit = %q, want empty", got)
	}
}

func TestMoveDataNeedsOneDirectoryRootAndSystemd(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	for _, tc := range []struct {
		args    []string
		root    bool
		systemd bool
		want    string
		code    int
	}{
		{nil, true, true, "usage", 2},
		{[]string{"a", "b"}, true, true, "usage", 2},
		{[]string{"/mnt/new"}, false, true, "as root", 1},
		{[]string{"/mnt/new"}, true, false, "systemd", 1},
		{[]string{"relative"}, true, true, "absolute", 1},
		{[]string{"/mnt/new disk"}, true, true, "must not contain", 1},
	} {
		var out, errb bytes.Buffer
		mv := mover{paths: paths, root: func() bool { return tc.root }, systemd: func() bool { return tc.systemd }, svc: &fakeService{},
			run: func(context.Context, datamove.Options) (datamove.Result, error) {
				t.Fatal("moved")
				return datamove.Result{}, nil
			}}
		if code := mv.main(tc.args, &out, &errb); code != tc.code || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%v: exit %d, %q; want %d and %q", tc.args, code, errb.String(), tc.code, tc.want)
		}
	}
}

func TestMoveDataHandsTheHiveToTheMove(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hiveconfig.LoadOrCreateKey(paths); err != nil {
		t.Fatal(err)
	}
	var got datamove.Options
	mv := mover{paths: paths, root: func() bool { return true }, systemd: func() bool { return true }, svc: &fakeService{},
		run: func(_ context.Context, o datamove.Options) (datamove.Result, error) {
			got = o
			return datamove.Result{}, errors.New("disk full")
		}}
	var out, errb bytes.Buffer
	if code := mv.main([]string{"/mnt/new/camhive"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "disk full") || !strings.Contains(errb.String(), "run the same command again") {
		t.Fatalf("exit %d, %q", code, errb.String())
	}
	if got.To != "/mnt/new/camhive" || got.From.Data != paths.Data || got.ArchiveDir != paths.Archive || len(got.StoreKey) != 32 || got.Service == nil || got.MinFreeBytes != 10<<30 {
		t.Fatalf("options = %+v", got)
	}
}

func TestMoveDataCancel(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	note := `{"state":"copying","to":"/mnt/new"}`
	if err := os.WriteFile(filepath.Join(paths.Data, datamove.File), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{}
	mv := mover{paths: paths, root: func() bool { return true }, systemd: func() bool { return true }, svc: svc}
	var out, errb bytes.Buffer
	if code := mv.main([]string{"--cancel"}, &out, &errb); code != 0 || datamove.Moving(paths.Data) || svc.restarts != 1 {
		t.Fatalf("exit %d, %q, restarted %d; want the move given up and the hive restarted to record", code, errb.String(), svc.restarts)
	}
}

type fakeService struct{ restarts int }

func (f *fakeService) Restart() error    { f.restarts++; return nil }
func (*fakeService) Switch(string) error { return nil }

// systemd is a fake systemctl: the commands it got, and whether the hive
// comes up after a restart.
type systemd struct {
	calls   []string
	startOK func(dropIn string) bool // given the drop-in's content at the restart
	dropIn  string
	up      bool
}

func (s *systemd) run(args ...string) error {
	s.calls = append(s.calls, strings.Join(args, " "))
	switch args[0] {
	case "cat":
		return nil
	case "is-active":
		if s.up {
			return nil
		}
		return errors.New("inactive")
	case "restart":
		b, _ := os.ReadFile(s.dropIn)
		s.up = s.startOK(string(b))
	}
	return nil
}

func newOSService(t *testing.T, sd *systemd) *osService {
	t.Helper()
	sd.dropIn = filepath.Join(t.TempDir(), serviceName+".service.d", dropInName)
	return &osService{systemctl: sd.run, dropIn: sd.dropIn, answering: func() error {
		if sd.up {
			return nil
		}
		return errors.New("not answering")
	}, wait: 50 * time.Millisecond}
}

// #155 review B3/B4: the new directory is written quoted into a drop-in,
// without uninstalling anything; the switch returns once the hive runs.
func TestASwitchWritesAQuotedDropInAndWaitsForTheHive(t *testing.T) {
	sd := &systemd{startOK: func(string) bool { return true }, up: true}
	s := newOSService(t, sd)
	if err := s.Switch("/mnt/new/camhive"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sd.dropIn)
	if err != nil || string(b) != "[Service]\nEnvironment=\"CAMHIVE_DATA_DIR=/mnt/new/camhive\"\n" {
		t.Fatalf("drop-in = %q, %v", b, err)
	}
	if strings.Join(sd.calls, ";") != "daemon-reload;restart camhive-hive;is-active --quiet camhive-hive" {
		t.Fatalf("systemctl calls = %v", sd.calls)
	}
}

// A hive that does not start from the new directory: the previous drop-in
// (none, here) comes back and the hive runs from the old place again.
func TestAFailedStartPutsTheOldDefinitionBack(t *testing.T) {
	sd := &systemd{startOK: func(dropIn string) bool { return !strings.Contains(dropIn, "/mnt/new") }, up: true}
	s := newOSService(t, sd)
	err := s.Switch("/mnt/new/camhive")
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("switch = %v, want the failed start", err)
	}
	if _, err := os.Stat(sd.dropIn); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the move's drop-in is still there: %v", err)
	}
	if !sd.up {
		t.Fatal("the hive was not started again from the old definition")
	}
	// With an earlier move's drop-in, that one comes back.
	if err := os.WriteFile(sd.dropIn, []byte(dropInFor("/srv/camhive")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Switch("/mnt/new/camhive"); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(sd.dropIn); string(b) != dropInFor("/srv/camhive") {
		t.Fatalf("drop-in after the failed switch = %q, want the earlier one", b)
	}
}

func TestRestartNeedsTheInstalledService(t *testing.T) {
	sd := &systemd{up: true}
	s := newOSService(t, sd)
	s.systemctl = func(args ...string) error {
		if args[0] == "cat" {
			return errors.New("No files found for camhive-hive.service.")
		}
		return nil
	}
	if err := s.Restart(); err == nil || !strings.Contains(err.Error(), "hive install") {
		t.Fatalf("restart without the service = %v", err)
	}
}

func TestStatusSaysAnUnfinishedMoveStopsRecording(t *testing.T) {
	dir := t.TempDir()
	if got := moveLine(dir); got != "" {
		t.Fatalf("without a move: %q", got)
	}
	note := `{"state":"copying","to":"/mnt/new","step":"archive","copied_bytes":1073741824,"planned_bytes":4294967296}`
	if err := os.WriteFile(filepath.Join(dir, datamove.File), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := moveLine(dir); !strings.Contains(got, "/mnt/new") || !strings.Contains(got, "not recording") || !strings.Contains(got, "25%") {
		t.Fatalf("during a move: %q", got)
	}
	note = `{"state":"switching","from":"/var/lib/camhive","to":"/mnt/new"}`
	if err := os.WriteFile(filepath.Join(dir, datamove.File), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := moveLine(dir); !strings.Contains(got, "did not finish") {
		t.Fatalf("a stuck switch: %q", got)
	}
}

// #155 re-review: --cancel from the new side puts the definition back as
// it was. A default install had no drop-in (hive.yaml in /etc/camhive):
// the drop-in goes, instead of one naming the old directory, which would
// hide /etc/camhive/hive.yaml. A hive moved before keeps its earlier one.
func TestCancelFromTheNewSideRestoresTheDefinitionAsItWas(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		sd := &systemd{startOK: func(string) bool { return true }, up: true}
		s := newOSService(t, sd)
		old, to := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Dir(sd.dropIn), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sd.dropIn, []byte(dropInFor(to)), 0o600); err != nil { // the stuck switch pointed it at the new directory
			t.Fatal(err)
		}
		oldNote := `{"state":"copying","from":"` + old + `","to":"` + to + `"}`
		newNote := fmt.Sprintf(`{"state":"switching","from":%q,"to":%q,"from_override":%v}`, old, to, earlier)
		if err := os.WriteFile(filepath.Join(old, datamove.File), []byte(oldNote), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, datamove.File), []byte(newNote), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := datamove.Cancel(to, s, time.Now()); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(sd.dropIn)
		switch {
		case !earlier && !errors.Is(err, os.ErrNotExist):
			t.Errorf("default install: drop-in after cancel = %q, %v; want none", b, err)
		case earlier && string(b) != dropInFor(old):
			t.Errorf("moved before: drop-in after cancel = %q, want the old directory's", b)
		}
		if !slices.Contains(sd.calls, "restart camhive-hive") || datamove.Moving(old) {
			t.Errorf("calls %v, old still moving %v; want a restart that records from the old directory", sd.calls, datamove.Moving(old))
		}
	}
}

func TestTheMoveRecordsWhetherAnEarlierMoveSetTheDirectory(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hiveconfig.LoadOrCreateKey(paths); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(t.TempDir(), dropInName)
	mv := mover{paths: paths, svc: &fakeService{}, dropIn: dropIn}
	if o, err := mv.options("/mnt/new", io.Discard); err != nil || o.FromOverride {
		t.Fatalf("without a drop-in: %v, override %v", err, o.FromOverride)
	}
	if err := os.WriteFile(dropIn, []byte(dropInFor(paths.Data)), 0o600); err != nil {
		t.Fatal(err)
	}
	if o, err := mv.options("/mnt/new", io.Discard); err != nil || !o.FromOverride {
		t.Fatalf("with a drop-in: %v, override %v", err, o.FromOverride)
	}
}
