package netscan

import (
	"bufio"
	"context"
	"crypto/md5" //nolint:gosec // RTSP digest is MD5 by specification
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNoDigest means the device asked for credentials but offered no digest
// challenge; the password is not sent in the clear (ADR-0017).
var ErrNoDigest = errors.New("netscan: the device offers no digest authentication")

// ErrUnauthorized means the device rejected the credentials.
var ErrUnauthorized = errors.New("netscan: credentials rejected")

// CheckRTSP asks the stream at rawURL for its description and proves the
// credentials with digest authentication only. A device that needs no
// credentials passes too.
func CheckRTSP(ctx context.Context, rawURL, user, pass string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "rtsp" {
		return fmt.Errorf("netscan: %q is not an rtsp URL", rawURL)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "554")
	}
	u.User = nil
	target := u.String()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	_ = conn.SetDeadline(deadline)
	r := textproto.NewReader(bufio.NewReader(conn))

	status, h, err := describe(conn, r, target, 1, "")
	if err != nil || status == 200 {
		return err
	}
	if status != 401 {
		return fmt.Errorf("netscan: DESCRIBE answered %d", status)
	}
	ch, ok := digestChallenge(h.Values("WWW-Authenticate"))
	if !ok {
		return ErrNoDigest
	}
	status, _, err = describe(conn, r, target, 2, ch.authorization(user, pass, "DESCRIBE", target))
	switch {
	case err != nil:
		return err
	case status == 200:
		return nil
	case status == 401:
		return ErrUnauthorized
	}
	return fmt.Errorf("netscan: DESCRIBE answered %d", status)
}

func describe(w io.Writer, r *textproto.Reader, target string, cseq int, auth string) (int, textproto.MIMEHeader, error) {
	req := fmt.Sprintf("DESCRIBE %s RTSP/1.0\r\nCSeq: %d\r\nAccept: application/sdp\r\nUser-Agent: camhive\r\n", target, cseq)
	if auth != "" {
		req += "Authorization: " + auth + "\r\n"
	}
	if _, err := io.WriteString(w, req+"\r\n"); err != nil {
		return 0, nil, err
	}
	line, err := r.ReadLine()
	if err != nil {
		return 0, nil, err
	}
	f := strings.Fields(line)
	if len(f) < 2 || !strings.HasPrefix(f[0], "RTSP/") {
		return 0, nil, fmt.Errorf("netscan: not an RTSP answer: %q", line)
	}
	status, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, nil, err
	}
	h, err := r.ReadMIMEHeader()
	if err != nil {
		return 0, nil, err
	}
	if n, _ := strconv.Atoi(h.Get("Content-Length")); n > 0 {
		if _, err := io.CopyN(io.Discard, r.R, int64(n)); err != nil {
			return 0, nil, err
		}
	}
	return status, h, nil
}

type challenge struct {
	realm, nonce, opaque string
	qopAuth              bool
}

// digestChallenge finds an MD5 digest challenge among WWW-Authenticate
// values (several schemes may share one header line).
func digestChallenge(values []string) (challenge, bool) {
	for _, v := range values {
		i := strings.Index(strings.ToLower(v), "digest ")
		if i < 0 {
			continue
		}
		p := params(v[i+len("digest "):])
		if alg := strings.ToUpper(p["algorithm"]); alg != "" && alg != "MD5" {
			continue
		}
		if p["nonce"] != "" {
			qop := false
			for _, q := range strings.Split(p["qop"], ",") {
				qop = qop || strings.TrimSpace(q) == "auth"
			}
			return challenge{realm: p["realm"], nonce: p["nonce"], opaque: p["opaque"], qopAuth: qop}, true
		}
	}
	return challenge{}, false
}

// params reads key="value" pairs, stopping at the next scheme name.
func params(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			if strings.Contains(part, " ") { // "Basic realm=…" begins another scheme
				break
			}
			continue
		}
		if strings.Contains(k, " ") {
			break
		}
		out[strings.ToLower(k)] = strings.Trim(v, `"`)
	}
	return out
}

func (c challenge) authorization(user, pass, method, uri string) string {
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) } //nolint:gosec // specification
	ha1, ha2 := h(user+":"+c.realm+":"+pass), h(method+":"+uri)
	out := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s"`, user, c.realm, c.nonce, uri)
	if c.qopAuth {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		cnonce := hex.EncodeToString(b)
		out += fmt.Sprintf(`, qop=auth, nc=00000001, cnonce="%s", response="%s"`, cnonce, h(ha1+":"+c.nonce+":00000001:"+cnonce+":auth:"+ha2))
	} else {
		out += fmt.Sprintf(`, response="%s"`, h(ha1+":"+c.nonce+":"+ha2))
	}
	if c.opaque != "" {
		out += fmt.Sprintf(`, opaque="%s"`, c.opaque)
	}
	return out
}
