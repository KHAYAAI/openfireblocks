package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

// requireToken protects every route but /health with a bearer token.
//
// Without it this server's /restore and /dr/failover would be callable by
// anything that can reach the pod -- overwriting the database, or promoting a
// replica and discarding data, on an unauthenticated POST. It fails closed:
// with no token configured every protected route answers 503 rather than
// serving, unless allowUnauthenticated is set, which exists for a developer's
// laptop and is refused in production by main.
func requireToken(next http.Handler, token string, allowUnauthenticated bool) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			if allowUnauthenticated {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "BACKUP_API_TOKEN is not configured, so this service refuses to act"})
			return
		}
		presented := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			presented = strings.TrimPrefix(h, "Bearer ")
		}
		got := sha256.Sum256([]byte(presented))
		// Compared as fixed-length digests so neither the length of the
		// secret nor an early mismatch is observable.
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			log.Printf("refused %s %s: missing or wrong bearer token", r.Method, r.URL.Path)
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorised"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
