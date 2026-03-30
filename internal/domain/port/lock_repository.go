package port

import (
	"context"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// LockRepository manages WOPI file lock persistence.
type LockRepository interface {
	// Create inserts or replaces a lock for a file.
	Create(ctx context.Context, lock *model.Lock) error
	// FindByFileID retrieves the active lock for a file. Returns nil if none.
	FindByFileID(ctx context.Context, fileID string) (*model.Lock, error)
	// UpdateLockID atomically replaces the lock ID and expiry for a file.
	UpdateLockID(ctx context.Context, fileID, newLockID string, newExpiry model.Lock) error
	// RefreshExpiry extends the lock expiry for a file.
	RefreshExpiry(ctx context.Context, fileID string, lock *model.Lock) error
	// DeleteByFileID removes the lock for a file.
	DeleteByFileID(ctx context.Context, fileID string) error
	// DeleteExpired removes all expired locks and returns the count deleted.
	DeleteExpired(ctx context.Context) (int64, error)
}
