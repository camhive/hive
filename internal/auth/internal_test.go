package auth

import (
	"errors"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
)

// Tokens signed by a trusted key but missing claims are still rejected.
func TestClaimsFromTokenRejectsMissingClaims(t *testing.T) {
	iss, _ := NewIssuer("k1")
	ver := NewVerifier("hv_1", Keys{"k1": iss.PublicKey()})
	now := time.Now()

	build := func(mut func(*paseto.Token)) string {
		tok := paseto.NewToken()
		tok.SetSubject("acc")
		tok.SetAudience("hv_1")
		tok.SetIssuedAt(now)
		tok.SetExpiration(now.Add(time.Minute))
		tok.SetString("sid", "s")
		tok.SetString("role", "viewer")
		_ = tok.Set("cams", []string{"*"})
		tok.SetFooter([]byte(`{"kid":"k1"}`))
		mut(&tok)
		return tok.V4Sign(iss.key, nil)
	}
	cases := map[string]func(*paseto.Token){
		"no sub":     func(tk *paseto.Token) { *tk = withoutClaim(*tk, "sub") },
		"no aud":     func(tk *paseto.Token) { *tk = withoutClaim(*tk, "aud") },
		"no iat":     func(tk *paseto.Token) { *tk = withoutClaim(*tk, "iat") },
		"no exp":     func(tk *paseto.Token) { *tk = withoutClaim(*tk, "exp") },
		"no sid":     func(tk *paseto.Token) { *tk = withoutClaim(*tk, "sid") },
		"no role":    func(tk *paseto.Token) { *tk = withoutClaim(*tk, "role") },
		"no cams":    func(tk *paseto.Token) { *tk = withoutClaim(*tk, "cams") },
		"bad role":   func(tk *paseto.Token) { tk.SetString("role", "god") },
		"bad kid":    func(tk *paseto.Token) { tk.SetFooter([]byte(`{"nokid":1}`)) },
		"bad footer": func(tk *paseto.Token) { tk.SetFooter([]byte(`not json`)) },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ver.Verify(build(mut)); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("got %v, want ErrInvalidToken", err)
			}
		})
	}
}

// withoutClaim rebuilds a token without one claim (go-paseto has no delete).
func withoutClaim(tok paseto.Token, drop string) paseto.Token {
	out := paseto.NewToken()
	for k, v := range tok.Claims() {
		if k != drop {
			_ = out.Set(k, v)
		}
	}
	out.SetFooter(tok.Footer())
	return out
}
