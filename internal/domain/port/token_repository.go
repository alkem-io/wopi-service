// Package port defines domain interfaces (driven and driving ports).
package port

import (
	"context"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// TokenRepository manages access token persistence.
type TokenRepository interface {
	// Create stores a new access token.
	Create(ctx context.Context, token *model.AccessToken) error
	// FindByToken retrieves a token by its opaque value. Returns nil if not found.
	FindByToken(ctx context.Context, tokenValue string) (*model.AccessToken, error)
	// DeleteByID removes a token by its UUID.
	DeleteByID(ctx context.Context, id string) error
	// DeleteExpired removes all expired tokens and returns the count deleted.
	DeleteExpired(ctx context.Context) (int64, error)
}
