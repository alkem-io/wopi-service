package port

import (
	"context"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// DocumentRepository provides read-only access to Alkemio's document table.
type DocumentRepository interface {
	// FindByID retrieves document metadata by UUID. Returns nil if not found.
	FindByID(ctx context.Context, documentID string) (*model.Document, error)
}
