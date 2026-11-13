package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHiveUpdateWritesARequestForTheUpdater(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := requestUpdate(dir, &out, &errb); code != 0 || !strings.Contains(out.String(), "update requested") {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "update-request")); err != nil {
		t.Fatal(err)
	}
	if code := requestUpdate(filepath.Join(dir, "missing"), &out, &errb); code != 1 || !strings.Contains(errb.String(), "does not run") {
		t.Fatalf("no updater: %d %q", code, errb.String())
	}
}
