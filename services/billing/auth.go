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
// These routes were open to anything that could reach the pod, including
// /v1/invoices/charge and /v1/billing/stripe-customer -- the latter lets a
// caller repoint a tenant's charges at another Stripe customer. "Admin-only by
// placement" meant "by network position", which is not a control.
//
// Fails closed: with no BILLING_API_TOKEN every protected route answers 503
// rather than serving. BILLING_ALLOW_UNAUTHENTICATED exists for a developer's
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
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "BILLING_API_TOKEN is not configured, so this service refuses to act"})
			return
		}
		presented := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			presented = strings.TrimPrefix(h, "Bearer ")
		}
		got := sha256.Sum256([]byte(presented))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			log.Printf("refused %s %s: missing or wrong bearer token", r.Method, r.URL.Path)
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorised"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
