package version_test

import (
	"testing"

	"github.com/camhive/hive/internal/version"
)

func TestString(t *testing.T) {
	cases := []struct {
		name string
		in   version.Info
		want string
	}{
		{"unset is dev", version.Info{}, "dev"},
		{"version only", version.Info{Version: "1.2.3"}, "1.2.3"},
		{"version and commit", version.Info{Version: "1.2.3", Commit: "abc1234"}, "1.2.3 (abc1234)"},
		{"version and date", version.Info{Version: "1.2.3", Date: "2026-11-08"}, "1.2.3 (2026-11-08)"},
		{"all fields", version.Info{Version: "1.2.3", Commit: "abc1234", Date: "2026-11-08"}, "1.2.3 (abc1234, 2026-11-08)"},
		{"dev with commit", version.Info{Commit: "abc1234"}, "dev (abc1234)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentIsDevForLocalBuild(t *testing.T) {
	// Nothing injects -ldflags in `go test`, so a local build is "dev".
	if got := version.Current().String(); got != "dev" {
		t.Fatalf("Current() = %q, want dev", got)
	}
}
