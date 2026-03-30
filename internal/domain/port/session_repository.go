package port

import (
	"context"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// SessionRepository manages WOPI editing session persistence.
type SessionRepository interface {
	// Create stores a new WOPI session.
	Create(ctx context.Context, session *model.WOPISession) error
	// FindByFileID retrieves all active sessions for a file.
	FindByFileID(ctx context.Context, fileID string) ([]model.WOPISession, error)
	// DeleteByTokenID removes sessions associated with a token.
	DeleteByTokenID(ctx context.Context, tokenID string) error
}
