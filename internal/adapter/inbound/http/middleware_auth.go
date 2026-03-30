package http

import (
	"context"
	"net/http"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

const tokenContextKey contextKey = "wopiToken"

// TokenAuthMiddleware validates WOPI access tokens from the access_token query parameter.
func TokenAuthMiddleware(tokenSvc *service.TokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenValue := r.URL.Query().Get("access_token")
			if tokenValue == "" {
				http.Error(w, `{"error":"missing access_token"}`, http.StatusUnauthorized)
				return
			}

			token, err := tokenSvc.ValidateToken(r.Context(), tokenValue)
			if err != nil {
				http.Error(w, `{"error":"token validation failed"}`, http.StatusInternalServerError)
				return
			}
			if token == nil {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), tokenContextKey, token)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TokenFromContext retrieves the validated WOPI access token set by TokenAuthMiddleware.
func TokenFromContext(ctx context.Context) *model.AccessToken {
	if v, ok := ctx.Value(tokenContextKey).(*model.AccessToken); ok {
		return v
	}
	return nil
}
