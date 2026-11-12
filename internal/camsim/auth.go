package camsim

import (
	"crypto/sha1" //nolint:gosec // WS-UsernameToken PasswordDigest is defined over SHA-1 (ONVIF Core Spec)
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"strings"
)

// usernameToken is the WS-Security UsernameToken an ONVIF client sends.
// Field tags match local names only, so any namespace prefix decodes.
type usernameToken struct {
	Username string `xml:"Username"`
	Digest   string `xml:"Password"` // base64
	Nonce    string `xml:"Nonce"`    // base64
	Created  string `xml:"Created"`
}

// parseUsernameToken extracts the token from a SOAP envelope, if present.
func parseUsernameToken(envelope string) (usernameToken, bool) {
	dec := xml.NewDecoder(strings.NewReader(envelope))
	for {
		t, err := dec.Token()
		if err != nil {
			return usernameToken{}, false
		}
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "UsernameToken" {
			continue
		}
		var tok usernameToken
		if err := dec.DecodeElement(&tok, &se); err != nil {
			return usernameToken{}, false
		}
		tok.Username = strings.TrimSpace(tok.Username)
		tok.Digest = strings.TrimSpace(tok.Digest)
		tok.Nonce = strings.TrimSpace(tok.Nonce)
		tok.Created = strings.TrimSpace(tok.Created)
		return tok, tok.Username != ""
	}
}

// verifyUsernameToken checks digest == Base64(SHA1(nonce + created + password)).
// Replay protection is intentionally absent: this is a simulator.
func verifyUsernameToken(tok usernameToken, user, pass string) bool {
	if subtle.ConstantTimeCompare([]byte(tok.Username), []byte(user)) != 1 {
		return false
	}
	nonce, err := base64.StdEncoding.DecodeString(tok.Nonce)
	if err != nil {
		return false
	}
	h := sha1.New() //nolint:gosec // see above
	h.Write(nonce)
	h.Write([]byte(tok.Created))
	h.Write([]byte(pass))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(tok.Digest), []byte(want)) == 1
}
