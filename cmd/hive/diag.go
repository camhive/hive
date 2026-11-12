package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/camhive/hive/internal/diag"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/version"
)

// runDiag writes the diagnostic bundle and prints what went into it. It
// opens the database read-only-ish (same key derivation) so a stopped hive
// can still be diagnosed.
func runDiag(paths hiveconfig.Paths, stdout, stderr *os.File) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	in := diag.Inputs{Paths: paths, Version: version.Current().String(), EngineVersion: engineVersion}
	if key, _, err := hiveconfig.LoadOrCreateKey(paths); err == nil {
		if sk, err := hiveconfig.StoreKey(key); err == nil {
			if st, err := store.Open(ctx, paths.DB, sk); err == nil {
				defer func() { _ = st.Close() }()
				in.Store = st
			} else {
				fmt.Fprintln(stderr, "diag: database not included:", err)
			}
		}
	}
	out, entries, err := diag.Build(ctx, in)
	if err != nil && out == "" {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "diagnostic bundle:", out)
	for _, e := range entries {
		fmt.Fprintln(stdout, "  ", e)
	}
	if err != nil {
		fmt.Fprintln(stderr, "warning:", err)
	}
	fmt.Fprintln(stdout, "Review the contents before sending the bundle to support. It contains no video, no keys and no passwords.")
	return 0
}

func engineVersion(ctx context.Context) string {
	bin := mediamtxBinary()
	if bin == "" {
		bin = "mediamtx"
	}
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput() //nolint:gosec // our own sidecar binary
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}
