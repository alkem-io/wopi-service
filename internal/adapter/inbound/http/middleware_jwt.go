// Package http implements inbound HTTP adapters for the WOPI service.
package http

import (
	"context"
	"net/http"
)

type contextKey string

const (
	actorIDKey   contextKey = "actorID"
	actorNameKey contextKey = "actorName"

	// HeaderActorID is stamped by Traefik's `alkemio-resolve` forwardAuth
	// middleware (alkemio-server's /api/auth/resolve). The gateway is
	// responsible for validating the request's credentials (cookie session
	// OR Hydra-issued bearer) before stamping. The chain
	// `strip-client-alkemio-headers` → `alkemio-resolve` ensures
	// client-supplied X-Alkemio-* are blanked before resolve overwrites
	// them; this header is server-trusted.
	HeaderActorID = "X-Alkemio-Actor-Id"
)

// ActorHeaderMiddleware extracts the actor id from the X-Alkemio-Actor-Id
// header set by the gateway. 401 if absent — the gateway didn't authenticate
// the request.
//
// Replaces the legacy `JWTMiddleware`, which decoded an Oathkeeper-minted
// JWT payload to read `alkemio_actor_id`. Identity is now established at
// the Traefik forwardAuth layer rather than via in-service JWT inspection.
//
// Display name: previously derived from Kratos session traits inlined into
// the Oathkeeper id_token. Not propagated by the new forwardAuth header.
// TODO: fetch display name via NATS AuthService (already wired in this
// service) when issuing a token; until then ActorNameFromContext returns "".
func ActorHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorID := r.Header.Get(HeaderActorID)
		if actorID == "" {
			http.Error(w, `{"error":"missing actor identity"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), actorIDKey, actorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ActorIDFromContext retrieves the actor ID set by ActorHeaderMiddleware.
func ActorIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorIDKey).(string); ok {
		return v
	}
	return ""
}

// ActorNameFromContext retrieves the actor display name if one was set on
// the context. Currently always returns "" — display name is no longer
// propagated via auth headers. The TokenHandler is responsible for
// resolving it from NATS AuthService when needed.
func ActorNameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorNameKey).(string); ok {
		return v
	}
	return ""
}
