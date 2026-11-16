package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
)

// #57: an owner at the hive's own console can switch the local login on
// even when the cloud is already gone, and off again.
func TestLocalLoginCommandSetsShowsAndDisables(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	cmd := func(stdin string, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := runLocalLogin(paths, args, strings.NewReader(stdin), &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, out, _ := cmd("", "status"); code != 0 || !strings.Contains(out, "disabled") {
		t.Fatalf("status on a fresh hive: %d %q", code, out)
	}
	if code, _, errs := cmd("short\n", "set", "--username", "anna"); code != 1 || !strings.Contains(errs, "10-256") {
		t.Fatalf("weak password: %d %q", code, errs)
	}
	if code, out, errs := cmd("correct horse battery\n", "set", "--username", "anna"); code != 0 || !strings.Contains(out, "enabled for anna") {
		t.Fatalf("set: %d %q %q", code, out, errs)
	}
	if code, out, _ := cmd("", "status"); code != 0 || !strings.Contains(out, "enabled for anna") {
		t.Fatalf("status after set: %d %q", code, out)
	}
	if code, out, _ := cmd("", "disable"); code != 0 || !strings.Contains(out, "disabled") {
		t.Fatalf("disable: %d %q", code, out)
	}
	if code, _, errs := cmd("", "rename"); code != 2 || !strings.Contains(errs, "usage") {
		t.Fatalf("unknown subcommand: %d %q", code, errs)
	}
	key, _, _ := hiveconfig.LoadOrCreateKey(paths)
	sk, _ := hiveconfig.StoreKey(key)
	st, err := store.Open(context.Background(), paths.DB, sk)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	acts, _ := st.Actions().Recent(context.Background(), 10)
	if len(acts) != 2 || acts[0].Action != "local_login.disable" || acts[1].Action != "local_login.enable" || acts[1].Actor != "console" {
		t.Fatalf("action log: %+v", acts)
	}
}

func TestPromptTwiceInsistsTheTwoPasswordsAgree(t *testing.T) {
	answers := func(in ...string) func() ([]byte, error) {
		return func() ([]byte, error) {
			if len(in) == 0 {
				return nil, errors.New("closed")
			}
			next := in[0]
			in = in[1:]
			return []byte(next), nil
		}
	}
	var prompt bytes.Buffer
	if got, err := promptTwice(answers("correct horse battery", "correct horse battery"), &prompt); err != nil || got != "correct horse battery" {
		t.Fatalf("same twice: %q %v", got, err)
	}
	if !strings.Contains(prompt.String(), "Password: ") || !strings.Contains(prompt.String(), "Again: ") {
		t.Fatalf("prompts: %q", prompt.String())
	}
	if _, err := promptTwice(answers("correct horse battery", "correct horse batterY"), &prompt); err == nil {
		t.Fatal("different passwords were accepted")
	}
	if _, err := promptTwice(answers("only once"), &prompt); err == nil {
		t.Fatal("a read error was ignored")
	}
}
