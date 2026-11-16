package journal_test

import (
	"path/filepath"
	"testing"

	"github.com/camhive/hive/internal/update/journal"
)

// `hive update` and the app's "Update now" (#82) ask through one file; the
// updater takes each request once.
func TestARequestIsTakenOnce(t *testing.T) {
	dir := t.TempDir()
	if asked, err := journal.TakeRequest(dir); asked || err != nil {
		t.Fatalf("nothing asked yet: %v %v", asked, err)
	}
	if err := journal.RequestUpdate(dir); err != nil {
		t.Fatal(err)
	}
	if err := journal.RequestUpdate(dir); err != nil { // asking twice is one request
		t.Fatal(err)
	}
	if asked, err := journal.TakeRequest(dir); !asked || err != nil {
		t.Fatalf("first take: %v %v", asked, err)
	}
	if asked, _ := journal.TakeRequest(dir); asked {
		t.Fatal("a request was taken twice")
	}
}

func TestNoRequestWithoutTheUpdatersDirectory(t *testing.T) {
	if err := journal.RequestUpdate(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("asked an updater that is not installed")
	}
}

// An updater that reads its settings at every check says so (#82); one
// that does not (v0.1.5) wrote nothing, and the app tells the owner a
// change needs a service restart.
func TestTheUpdaterAnnouncesItself(t *testing.T) {
	dir := t.TempDir()
	if v, err := journal.Announced(dir); v != "" || err != nil {
		t.Fatalf("before = %q %v", v, err)
	}
	if err := journal.Announce(dir, "0.1.6"); err != nil {
		t.Fatal(err)
	}
	if v, err := journal.Announced(dir); v != "0.1.6" || err != nil {
		t.Fatalf("after = %q %v", v, err)
	}
}
