package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"aidanwoods.dev/go-paseto"
)

// LocalTTL is the lifetime of a token the hive issues after a local login
// (#57): long enough to look through recordings, short enough that a token
// copied off a LAN device is soon useless.
const LocalTTL = time.Hour

// localPurpose is the PASETO implicit assertion of local tokens: a token
// encrypted for any other purpose with the same key does not decrypt.
var localPurpose = []byte("camhive hive local login v1")

const localPrefix = "v4.local."

// IsLocalToken tells a hive-issued local token from a cloud token by its
// PASETO header; it does not verify anything.
func IsLocalToken(token string) bool { return strings.HasPrefix(token, localPrefix) }

// LocalSessions issues and verifies the hive's own owner tokens after a
// local login, the emergency path when the cloud is unreachable (ADR-0004).
// They are PASETO v4.local: encrypted and authenticated with a symmetric
// key that lives only in this process, so the cloud verifier (v4.public)
// cannot accept them and this one cannot accept cloud tokens. A restart
// ends every local session; the hive also checks Claims.Credential against
// the stored credential (internal/hiveapp).
type LocalSessions struct {
	hiveID string
	now    func() time.Time
	key    paseto.V4SymmetricKey
}

// NewLocalSessions creates the issuer for this hive with a fresh key.
func NewLocalSessions(hiveID string, now func() time.Time) *LocalSessions {
	return &LocalSessions{hiveID: hiveID, now: now, key: paseto.NewV4SymmetricKey()}
}

// Issue returns an owner token for every camera, valid for LocalTTL, bound
// to the version of the credential the owner signed in with.
func (s *LocalSessions) Issue(account, credential string) (string, Claims, error) {
	sid := make([]byte, 16)
	if _, err := rand.Read(sid); err != nil {
		return "", Claims{}, err
	}
	now := s.now()
	c := Claims{Account: account, HiveID: s.hiveID, SessionID: "local_" + hex.EncodeToString(sid), Cameras: []string{"*"},
		Role: RoleOwner, Local: true, Credential: credential, IssuedAt: now, Expiry: now.Add(LocalTTL)}
	tok, err := toToken(c)
	if err != nil {
		return "", Claims{}, err
	}
	return tok.V4Encrypt(s.key, localPurpose), c, nil
}

// Verify decrypts a local token and checks its lifetime and audience.
func (s *LocalSessions) Verify(token string) (Claims, error) {
	if !IsLocalToken(token) {
		return Claims{}, fmt.Errorf("%w: not a local token", ErrInvalidToken)
	}
	tok, err := paseto.NewParserWithoutExpiryCheck().ParseV4Local(s.key, token, localPurpose)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	c, err := validClaims(tok, true)
	if err != nil {
		return Claims{}, err
	}
	// The key is this hive's and this process's alone, so the audience
	// cannot differ; time is all that is left to check.
	if err := c.checkTime(s.now()); err != nil {
		return Claims{}, err
	}
	return c, nil
}
