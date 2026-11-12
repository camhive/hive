package hiveapp

import "net/http"

// cors lets the web app, served from the cloud origin, call the hive
// directly over the LAN name or through the relay (ADR-0010/0012). The API
// authenticates with bearer tokens, never cookies, so any origin may ask;
// the token decides. The preflight also answers Chrome's private-network
// access check (Access-Control-Allow-Private-Network) so a page on a public
// origin may reach a LAN address.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length, Content-Range, X-Camhive-Stream")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
			h.Set("Access-Control-Allow-Private-Network", "true")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
