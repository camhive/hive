package auth

// Passwords: the policy and argon2id hashing shared by cloud accounts
// (internal/domain/account, internal/cloud/accounts) and the hive's local
// login (#57). It lives here, in a package the public hive tree carries
// (ADR-0014), so both sides use one implementation. Stored hashes are PHC
// strings; their format and the default parameters must not change, or
// existing hashes stop verifying.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password policy (NIST 800-63B: length over composition rules).
const (
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// ErrWeakPassword rejects passwords that fail the policy.
var ErrWeakPassword = errors.New("auth: password must be 10-256 characters and not the e-mail address or username")

// CheckPassword applies the policy; the password may not be the account's
// e-mail address or the local username (identity), compared case-insensitively.
func CheckPassword(password, identity string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLen || n > MaxPasswordLen {
		return ErrWeakPassword
	}
	if strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(identity)) {
		return ErrWeakPassword
	}
	return nil
}

// Argon2Params are the argon2id cost parameters (OWASP 2023 minimum:
// 19 MiB, 2 iterations, 1 lane). Tests lower them through DefaultArgon2.
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	KeyLen  uint32
}

// DefaultArgon2 is used by HashPassword.
var DefaultArgon2 = Argon2Params{Memory: 19 * 1024, Time: 2, Threads: 1, KeyLen: 32}

// HashPassword returns a PHC string: $argon2id$v=19$m=...,t=...,p=...$salt$hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := DefaultArgon2
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// ErrBadHash means the stored hash is not a PHC argon2id string.
var ErrBadHash = errors.New("auth: malformed password hash")

// VerifyPassword compares in constant time; false on mismatch, error only
// on a malformed hash.
func VerifyPassword(phc, password string) (bool, error) {
	p, salt, want, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) //nolint:gosec // length bounded by hash
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// parsePHC reads $argon2id$v=19$m=..,t=..,p=..$salt$hash; ErrBadHash for
// anything else, including zero parameters or an empty hash.
func parsePHC(phc string) (p Argon2Params, salt, hash []byte, err error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return p, nil, nil, ErrBadHash
	}
	p, ok := phcParams(parts[3])
	if !ok {
		return p, nil, nil, ErrBadHash
	}
	enc := base64.RawStdEncoding
	salt, saltErr := enc.DecodeString(parts[4])
	hash, hashErr := enc.DecodeString(parts[5])
	if saltErr != nil || hashErr != nil || len(hash) == 0 {
		return p, nil, nil, ErrBadHash
	}
	return p, salt, hash, nil
}

// phcParams reads m=..,t=..,p=..; none of them may be zero.
func phcParams(s string) (Argon2Params, bool) {
	var p Argon2Params
	_, err := fmt.Sscanf(s, "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads)
	return p, err == nil && p.Memory > 0 && p.Time > 0 && p.Threads > 0
}

// NeedsRehash reports whether the stored hash uses weaker parameters than
// DefaultArgon2, so the service can upgrade it after a successful login.
func NeedsRehash(phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 {
		return true
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return true
	}
	d := DefaultArgon2
	return p.Memory < d.Memory || p.Time < d.Time
}
