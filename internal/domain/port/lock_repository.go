package port

import (
	"context"
	"errors"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// ErrStaleLock is returned by CAS lock operations when the expected lock_id
// no longer matches (concurrent modification). Adapters MUST return this error
// when a conditional write affects zero rows.
var ErrStaleLock = errors.New("stale lock: concurrent modification detected")

// LockRepository manages WOPI file lock persistence.
// Write operations use compare-and-swap (CAS) on lock_id to prevent races.
type LockRepository interface {
	// Create inserts or replaces a lock for a file.
	Create(ctx context.Context, lock *model.Lock) error
	// FindByFileID retrieves the active (non-expired) lock for a file. Returns nil if none.
	FindByFileID(ctx context.Context, fileID string) (*model.Lock, error)
	// UpdateLockID atomically replaces the lock ID (CAS: only if currentLockID matches).
	UpdateLockID(ctx context.Context, fileID, currentLockID, newLockID string, newExpiry model.Lock) error
	// RefreshExpiry extends the lock expiry (CAS: only if lockID matches).
	RefreshExpiry(ctx context.Context, fileID, lockID string, lock *model.Lock) error
	// DeleteByFileID removes the lock (CAS: only if lockID matches).
	DeleteByFileID(ctx context.Context, fileID, lockID string) error
	// DeleteExpired removes all expired locks and returns the count deleted.
	DeleteExpired(ctx context.Context) (int64, error)
}
