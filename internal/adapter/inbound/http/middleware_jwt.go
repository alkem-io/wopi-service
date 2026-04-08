// Package http implements inbound HTTP adapters for the WOPI service.
package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const actorIDKey contextKey = "actorID"

// JWTMiddleware extracts the alkemio_actor_id from the Oathkeeper-injected JWT.
// Oathkeeper has already validated the JWT — we only parse the payload to
// extract the actor ID without re-validating the signature.
func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
			return
		}

		actorID, err := extractActorIDFromJWT(parts[1])
		if err != nil || actorID == "" {
			http.Error(w, `{"error":"invalid token: missing actor ID"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), actorIDKey, actorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ActorIDFromContext retrieves the actor ID set by JWTMiddleware.
func ActorIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorIDKey).(string); ok {
		return v
	}
	return ""
}

type jwtClaims struct {
	AlkemioActorID string `json:"alkemio_actor_id"`
}

func extractActorIDFromJWT(tokenString string) (string, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return "", nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}

	return claims.AlkemioActorID, nil
}
