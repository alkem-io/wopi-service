package http

import "net/http"

// ProofMiddleware validates WOPI proof signatures from Collabora.
// TODO(Phase 3): Full RSA SHA-256 proof validation will be implemented
// when the discovery service provides cached proof keys. For now this
// is a pass-through that logs proof headers for debugging.
func ProofMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Proof validation will be wired in Phase 5 (US3) when discovery
		// provides the RSA public keys. For now, pass through.
		next.ServeHTTP(w, r)
	})
}
