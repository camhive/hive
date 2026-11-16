package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
)

const localLoginUsage = "usage: hive local-login status | set --username NAME (password on stdin) | disable"

// runLocalLogin manages the owner's local emergency login (#57) from the
// hive's own console, for the owner who never enabled it in the app and
// now has no cloud: whoever runs this as the hive's administrator owns the
// hardware anyway. A running hive reads the row on every local request, so
// a change or disable here also ends the local sessions already open.
func runLocalLogin(paths hiveconfig.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, localLoginUsage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch args[0] {
	case "status", "set", "disable":
	default:
		fmt.Fprintln(stderr, localLoginUsage)
		return 2
	}
	st, err := openStore(ctx, paths)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = st.Close() }()
	switch args[0] {
	case "set":
		return setLocalLogin(ctx, st, args[1:], stdin, stdout, stderr)
	case "disable":
		return disableLocalLogin(ctx, st, stdout, stderr)
	}
	return localLoginStatus(ctx, st, stdout, stderr)
}

func openStore(ctx context.Context, paths hiveconfig.Paths) (*store.Store, error) {
	key, _, err := hiveconfig.LoadOrCreateKey(paths)
	if err != nil {
		return nil, err
	}
	sk, err := hiveconfig.StoreKey(key)
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, paths.DB, sk)
}

func localLoginStatus(ctx context.Context, st *store.Store, stdout, stderr io.Writer) int {
	c, err := st.LocalLogin().Get(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fmt.Fprintln(stdout, "local login: disabled")
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 1
	default:
		fmt.Fprintf(stdout, "local login: enabled for %s since %s\n", c.Username, c.UpdatedAt.Format(time.RFC3339))
	}
	return 0
}

func setLocalLogin(ctx context.Context, st *store.Store, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hive local-login set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	username := fs.String("username", "", "local username")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	password, err := readPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	c, err := hiveapp.NewLocalCredential(*username, password, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := st.LocalLogin().Set(ctx, c); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	details, _ := json.Marshal(map[string]any{"username": c.Username})
	if err := st.Actions().Append(ctx, store.Action{Actor: "console", Action: "local_login.enable", Details: details}); err != nil {
		fmt.Fprintln(stderr, "action log:", err)
	}
	fmt.Fprintf(stdout, "local login: enabled for %s\n", c.Username)
	return 0
}

func disableLocalLogin(ctx context.Context, st *store.Store, stdout, stderr io.Writer) int {
	if err := st.LocalLogin().Delete(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := st.Actions().Append(ctx, store.Action{Actor: "console", Action: "local_login.disable"}); err != nil {
		fmt.Fprintln(stderr, "action log:", err)
	}
	fmt.Fprintln(stdout, "local login: disabled")
	return 0
}

// readPassword asks twice without echo on a terminal and reads one line
// otherwise (a pipe, a script).
func readPassword(stdin io.Reader, prompt io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) { //nolint:gosec // a file descriptor fits an int
		return promptTwice(func() ([]byte, error) { return term.ReadPassword(int(f.Fd())) }, prompt) //nolint:gosec // as above
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// promptTwice reads the password twice with read (no echo) and insists the
// two agree.
func promptTwice(read func() ([]byte, error), prompt io.Writer) (string, error) {
	var got [2]string
	for i, label := range []string{"Password: ", "Again: "} {
		fmt.Fprint(prompt, label)
		b, err := read()
		fmt.Fprintln(prompt)
		if err != nil {
			return "", err
		}
		got[i] = string(b)
	}
	if got[0] != got[1] {
		return "", errors.New("the passwords differ")
	}
	return got[0], nil
}
