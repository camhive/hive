package auth_test

import (
	"strings"
	"testing"

	"github.com/camhive/hive/internal/auth"
)

func TestPasswordPolicyAndHashing(t *testing.T) {
	auth.DefaultArgon2 = auth.Argon2Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32}
	for _, bad := range []string{"short", strings.Repeat("x", 257), "Anna@Example.de"} {
		if err := auth.CheckPassword(bad, "anna@example.de"); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if err := auth.CheckPassword("correct horse battery", "anna@example.de"); err != nil {
		t.Fatal(err)
	}
	phc, err := auth.HashPassword("correct horse battery")
	if err != nil || !strings.HasPrefix(phc, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("hash = %q err=%v", phc, err)
	}
	if ok, err := auth.VerifyPassword(phc, "correct horse battery"); !ok || err != nil {
		t.Fatalf("verify = %v %v", ok, err)
	}
	if ok, _ := auth.VerifyPassword(phc, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=1,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=x$AAAA$AAAA", "$argon2id$v=19$m=1,t=1,p=1$!!$AAAA", "$argon2id$v=19$m=0,t=1,p=1$AAAA$AAAA"} {
		if _, err := auth.VerifyPassword(bad, "x"); err == nil {
			t.Fatalf("%q must be malformed", bad)
		}
	}
	if auth.NeedsRehash(phc) {
		t.Fatal("current params need no rehash")
	}
	auth.DefaultArgon2.Memory = 2048
	if !auth.NeedsRehash(phc) || !auth.NeedsRehash("garbage") {
		t.Fatal("weaker params must be rehashed")
	}
}

// A hash stored before the password code moved here from
// internal/domain/account (made with DefaultArgon2 then: 19 MiB, 2 passes,
// 1 lane) must keep verifying: cloud accounts and local logins hold such
// hashes.
const storedBeforeTheMove = "$argon2id$v=19$m=19456,t=2,p=1$om2xJ54/GmFSwzklYWqyrQ$mADhQdl988KY7qVOZ0gM/2gCijOsHFzAizzwrQlK+bk"

func TestHashStoredBeforeTheMoveStillVerifies(t *testing.T) {
	if ok, err := auth.VerifyPassword(storedBeforeTheMove, "correct horse battery"); !ok || err != nil {
		t.Fatalf("verify = %v %v", ok, err)
	}
	if ok, _ := auth.VerifyPassword(storedBeforeTheMove, "correct horse batterY"); ok {
		t.Fatal("a wrong password verified")
	}
}
