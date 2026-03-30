// Package alkemiodb implements read-only access to Alkemio's PostgreSQL database.
package alkemiodb

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// DocumentRepository looks up document metadata from Alkemio's database.
type DocumentRepository struct {
	pool *pgxpool.Pool
}

// NewDocumentRepository creates a new DocumentRepository.
func NewDocumentRepository(pool *pgxpool.Pool) *DocumentRepository {
	return &DocumentRepository{pool: pool}
}

// FindByID retrieves document metadata by UUID. Returns nil if not found.
func (r *DocumentRepository) FindByID(ctx context.Context, documentID string) (*model.Document, error) {
	var doc model.Document
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, "externalID", "displayName", "mimeType", size, "authorizationId"::text
		 FROM document
		 WHERE id = $1`,
		documentID,
	).Scan(
		&doc.ID,
		&doc.ExternalID,
		&doc.DisplayName,
		&doc.MimeType,
		&doc.Size,
		&doc.AuthorizationPolicyID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &doc, nil
}
