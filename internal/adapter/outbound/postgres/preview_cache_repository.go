package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres/generated"
	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// PreviewCacheRepository implements port.PreviewCacheRepository using
// PostgreSQL.
type PreviewCacheRepository struct {
	db generated.DBTX
}

// NewPreviewCacheRepository creates a new PreviewCacheRepository.
func NewPreviewCacheRepository(db generated.DBTX) *PreviewCacheRepository {
	return &PreviewCacheRepository{db: db}
}

// FindBySourceID returns the current mapping for a source file, or nil if
// none exists.
func (r *PreviewCacheRepository) FindBySourceID(ctx context.Context, sourceFileID string) (*model.PreviewCacheEntry, error) {
	q := generated.New(r.db)
	row, err := q.FindPreviewCacheBySourceID(ctx, sourceFileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &model.PreviewCacheEntry{
		SourceFileID:      row.SourceFileID,
		PreviewFileID:     row.PreviewFileID,
		SourceUpdatedDate: row.SourceUpdatedDate.Time,
	}, nil
}

// Upsert commits preview_file_id and source_updated_date together in one
// atomic write.
func (r *PreviewCacheRepository) Upsert(ctx context.Context, entry model.PreviewCacheEntry) error {
	q := generated.New(r.db)
	return q.UpsertPreviewCache(ctx, generated.UpsertPreviewCacheParams{
		SourceFileID:      entry.SourceFileID,
		PreviewFileID:     entry.PreviewFileID,
		SourceUpdatedDate: timestamptzFromTime(entry.SourceUpdatedDate),
	})
}
