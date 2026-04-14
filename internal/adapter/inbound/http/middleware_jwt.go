// Package http implements inbound HTTP adapters for the WOPI service.
package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type contextKey string

const (
	actorIDKey   contextKey = "actorID"
	actorNameKey contextKey = "actorName"
)

// JWTMiddleware extracts the alkemio_actor_id and user display name from the
// Oathkeeper-injected JWT. Oathkeeper has already validated the JWT — we only
// parse the payload to extract claims without re-validating the signature.
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

		claims, err := extractClaimsFromJWT(parts[1])
		if err != nil || claims.AlkemioActorID == "" {
			http.Error(w, `{"error":"invalid token: missing actor ID"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), actorIDKey, claims.AlkemioActorID)
		ctx = context.WithValue(ctx, actorNameKey, claims.ActorDisplayName())
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

// ActorNameFromContext retrieves the actor display name set by JWTMiddleware.
func ActorNameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorNameKey).(string); ok {
		return v
	}
	return ""
}

type jwtClaims struct {
	AlkemioActorID string     `json:"alkemio_actor_id"`
	Session        jwtSession `json:"session"`
}

type jwtSession struct {
	Identity jwtIdentity `json:"identity"`
}

type jwtIdentity struct {
	Traits jwtTraits `json:"traits"`
}

type jwtTraits struct {
	Name  jwtName `json:"name"`
	Email string  `json:"email"`
}

type jwtName struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

// ActorDisplayName returns a human-readable display name from the JWT claims.
func (c *jwtClaims) ActorDisplayName() string {
	first := strings.TrimSpace(c.Session.Identity.Traits.Name.First)
	last := strings.TrimSpace(c.Session.Identity.Traits.Name.Last)

	if first != "" && last != "" {
		return first + " " + last
	}
	if first != "" {
		return first
	}
	if last != "" {
		return last
	}
	// Fallback to email if no name
	if c.Session.Identity.Traits.Email != "" {
		return c.Session.Identity.Traits.Email
	}
	return ""
}

func extractClaimsFromJWT(tokenString string) (*jwtClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}

	return &claims, nil
}
