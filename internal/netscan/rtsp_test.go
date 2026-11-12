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

func TestOnlyMD5DigestIsAnswered(t *testing.T) {
	if _, ok := digestChallenge([]string{`Digest realm="r", nonce="n", algorithm=MD5`}); !ok {
		t.Fatal("explicit MD5 refused")
	}
	if _, ok := digestChallenge([]string{`Digest realm="r", nonce="n", algorithm=SHA-256`}); ok {
		t.Fatal("SHA-256 accepted; the response would be computed with MD5")
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

// answers replays canned answers to whatever is sent.
type answers struct {
	in  strings.Builder
	out *strings.Reader
}

func (a *answers) Read(p []byte) (int, error)  { return a.out.Read(p) }
func (a *answers) Write(p []byte) (int, error) { return a.in.Write(p) }

func TestHandshakeOddAnswers(t *testing.T) {
	sdp := "v=0\r\n"
	for name, tc := range map[string]struct {
		reply string
		ok    bool
	}{
		"200 with an SDP body":        {fmt.Sprintf("RTSP/1.0 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp), true},
		"an HTTP server":              {"HTTP/1.1 400 Bad Request\r\n\r\n", false},
		"a broken status":             {"RTSP/1.0 abc\r\n\r\n", false},
		"not found":                   {"RTSP/1.0 404 Not Found\r\n\r\n", false},
		"refused after the challenge": {"RTSP/1.0 401 x\r\nWWW-Authenticate: Digest realm=\"r\", nonce=\"n\"\r\n\r\nRTSP/1.0 403 Forbidden\r\n\r\n", false},
		"a cut-off body":              {"RTSP/1.0 200 OK\r\nContent-Length: 50\r\n\r\nv=0", false},
		"silence":                     {"", false},
		"no reason phrase":            {"RTSP/1.0 200\r\n\r\n", true},
	} {
		err := handshake(&answers{out: strings.NewReader(tc.reply)}, "rtsp://h/x", "u", "p")
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRTSPTargetDropsCredentialsAndDefaultsThePort(t *testing.T) {
	target, host, err := rtspTarget("rtsp://admin:pw@10.0.0.5/live")
	if err != nil || target != "rtsp://10.0.0.5/live" || host != "10.0.0.5:554" {
		t.Fatalf("%q %q %v", target, host, err)
	}
	for _, bad := range []string{"http://h/x", "rtsp://", "::"} {
		if _, _, err := rtspTarget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
		if err := CheckRTSP(context.Background(), bad, "u", "p"); err == nil {
			t.Errorf("CheckRTSP %q accepted", bad)
		}
	}
	if err := CheckRTSP(ctx(t), "rtsp://127.0.0.1:1/x", "u", "p"); err == nil {
		t.Fatal("dialled a closed port without an error")
	}
}
