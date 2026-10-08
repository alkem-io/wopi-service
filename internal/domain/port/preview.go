package port

import (
	"context"
	"io"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// PreviewCacheRepository stores the one current source-file → preview-file
// mapping and the source updatedDate it was rendered from.
type PreviewCacheRepository interface {
	// FindBySourceID returns the current mapping, or nil if none exists.
	FindBySourceID(ctx context.Context, sourceFileID string) (*model.PreviewCacheEntry, error)
	// Upsert commits both fields together in one atomic write.
	Upsert(ctx context.Context, entry model.PreviewCacheEntry) error
}

// ThumbnailRenderer renders a preview via Collabora's thumbnail endpoint.
type ThumbnailRenderer interface {
	// Render streams content and returns the PNG stream the caller must close.
	Render(ctx context.Context, extension string, content io.Reader) (io.ReadCloser, error)
}
