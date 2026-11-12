package netscan

import (
	"bufio"
	"context"
	"crypto/md5" //nolint:gosec // RTSP digest is MD5 by specification
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// fakeRTSP answers DESCRIBE: unauthenticated with the given challenge, then
// 200 when the Authorization header carries the right digest for user/pass.
func fakeRTSP(t *testing.T, challenge, user, pass string) (url string, seen *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	auths := []string{}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := textproto.NewReader(bufio.NewReader(c))
		for {
			line, err := r.ReadLine()
			if err != nil {
				return
			}
			uri := strings.Fields(line)[1]
			h, _ := r.ReadMIMEHeader()
			auth := h.Get("Authorization")
			auths = append(auths, auth)
			status := "401 Unauthorized"
			if challenge == "" || auth == wantDigest(user, pass, uri) {
				status = "200 OK"
			}
			fmt.Fprintf(c, "RTSP/1.0 %s\r\nCSeq: %s\r\n", status, h.Get("CSeq"))
			if status != "200 OK" {
				fmt.Fprintf(c, "WWW-Authenticate: %s\r\n", challenge)
			}
			fmt.Fprint(c, "Content-Length: 0\r\n\r\n")
		}
	}()
	return "rtsp://" + ln.Addr().String() + "/main", &auths
}

func wantDigest(user, pass, uri string) string {
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) } //nolint:gosec // specification
	resp := h(h(user+":cam:"+pass) + ":n1:" + h("DESCRIBE:"+uri))
	return fmt.Sprintf(`Digest username="%s", realm="cam", nonce="n1", uri="%s", response="%s"`, user, uri, resp)
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestCheckRTSPAnswersADigestChallenge(t *testing.T) {
	url, _ := fakeRTSP(t, `Basic realm="cam", Digest realm="cam", nonce="n1"`, "admin", "pw")
	if err := CheckRTSP(ctx(t), url, "admin", "pw"); err != nil {
		t.Fatal(err)
	}
	url, _ = fakeRTSP(t, `Digest realm="cam", nonce="n1"`, "admin", "pw")
	if err := CheckRTSP(ctx(t), url, "admin", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

// ADR-0017: a device that offers only Basic never sees the password.
func TestDigestWithQopAuthUsesACnonce(t *testing.T) {
	ch, ok := digestChallenge([]string{`Digest realm="cam", nonce="n1", qop="auth,auth-int", opaque="o"`})
	if !ok || !ch.qopAuth {
		t.Fatalf("challenge = %+v", ch)
	}
	a := ch.authorization("admin", "pw", "DESCRIBE", "rtsp://h/x")
	for _, want := range []string{"qop=auth", "nc=00000001", `cnonce="`, `opaque="o"`} {
		if !strings.Contains(a, want) {
			t.Errorf("%q lacks %q", a, want)
		}
	}
}

func TestCheckRTSPNeverSendsBasic(t *testing.T) {
	url, seen := fakeRTSP(t, `Basic realm="cam"`, "admin", "pw")
	if err := CheckRTSP(ctx(t), url, "admin", "pw"); !errors.Is(err, ErrNoDigest) {
		t.Fatalf("err = %v", err)
	}
	for _, a := range *seen {
		if a != "" {
			t.Fatalf("sent Authorization %q", a)
		}
	}
}

func TestCheckRTSPWithoutAuthIsFine(t *testing.T) {
	url, _ := fakeRTSP(t, "", "", "")
	if err := CheckRTSP(ctx(t), url, "admin", "pw"); err != nil {
		t.Fatal(err)
	}
}
