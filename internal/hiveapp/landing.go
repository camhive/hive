package hiveapp

import (
	"net/http"
	"strings"
)

// The landing page (ADR-0023) is what loads after the user opened the hive
// at its LAN address and proceeded through the browser's certificate
// warning. It needs no token, so it carries nothing about the hive (no
// version, id or name, no links): it only tells the user that the browser
// now reaches the hive and to go back to the app, which probes the address
// again. It exists on the LAN listener only; elsewhere the root is a 404.

const landingEN = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Camhive hive</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32rem; margin: 3rem auto; padding: 0 1rem; line-height: 1.5">
<h1>Your browser can now reach this hive</h1>
<p>Go back to the Camhive app and press <b>Try again</b>. You can close this tab.</p>
</body></html>
`

const landingDE = `<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Camhive Hive</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32rem; margin: 3rem auto; padding: 0 1rem; line-height: 1.5">
<h1>Ihr Browser erreicht diesen Hive jetzt</h1>
<p>Kehren Sie zur Camhive-App zurück und tippen Sie auf <b>Erneut versuchen</b>. Diesen Tab können Sie schließen.</p>
</body></html>
`

func landing(w http.ResponseWriter, r *http.Request) {
	if !onLANListener(r) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	page := landingEN
	if prefersGerman(r.Header.Get("Accept-Language")) {
		page = landingDE
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(page))
}

// prefersGerman reads the first language of an Accept-Language header;
// browsers list them most preferred first.
func prefersGerman(header string) bool {
	first, _, _ := strings.Cut(header, ",")
	tag, _, _ := strings.Cut(strings.TrimSpace(first), ";")
	return strings.EqualFold(tag, "de") || strings.HasPrefix(strings.ToLower(tag), "de-")
}
