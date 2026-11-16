package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
)

func TestLocalSessionGivesTheOwnerEveryCameraForAnHour(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := auth.NewLocalSessions("hv_1", func() time.Time { return now })
	tok, issued, err := s.Issue("local:anna", "v1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != auth.RoleOwner || !got.Local || got.Account != "local:anna" || got.HiveID != "hv_1" || got.Credential != "v1" {
		t.Fatalf("claims %+v", got)
	}
	if !got.Allows("cam-1", auth.ActionArchive) || !got.Allows("", auth.ActionManage) {
		t.Fatal("a local owner session must reach every camera and manage the hive")
	}
	if !got.Expiry.Equal(now.Add(auth.LocalTTL)) || !issued.Expiry.Equal(got.Expiry) || got.SessionID == "" || got.SessionID != issued.SessionID {
		t.Fatalf("issued %+v, verified %+v", issued, got)
	}
}

func TestLocalSessionExpires(t *testing.T) {
	now := time.Now()
	s := auth.NewLocalSessions("hv_1", func() time.Time { return now })
	tok, _, _ := s.Issue("local:anna", "v1")
	now = now.Add(auth.LocalTTL)
	if _, err := s.Verify(tok); !errors.Is(err, auth.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

// A local token is a different kind of token, not a cloud token with a
// flag: the cloud verifier cannot read it and the local verifier refuses
// cloud tokens, another hive's local tokens and anything forged.
func TestLocalAndCloudTokensCannotBeConfused(t *testing.T) {
	iss, _ := auth.NewIssuer("k1")
	cloud := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()})
	local := auth.NewLocalSessions("hv_1", time.Now)
	other := auth.NewLocalSessions("hv_1", time.Now)

	localTok, _, _ := local.Issue("local:anna", "v1")
	if !auth.IsLocalToken(localTok) || !strings.HasPrefix(localTok, "v4.local.") {
		t.Fatalf("local token %q", localTok)
	}
	if _, err := cloud.Verify(localTok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("cloud verifier accepted a local token: %v", err)
	}
	now := time.Now()
	cloudTok, _ := iss.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "s", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now, Expiry: now.Add(time.Minute)})
	if auth.IsLocalToken(cloudTok) {
		t.Fatal("a cloud token looks local")
	}
	for name, tok := range map[string]string{"cloud token": cloudTok, "another hive's key": mustIssue(t, other), "garbage": "v4.local.AAAA"} {
		if _, err := local.Verify(tok); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func mustIssue(t *testing.T, s *auth.LocalSessions) string {
	t.Helper()
	tok, _, err := s.Issue("local:anna", "v1")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
