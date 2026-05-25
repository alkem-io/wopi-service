package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres/generated"
	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// LockRepository implements port.LockRepository using PostgreSQL.
type LockRepository struct {
	db generated.DBTX
}

// NewLockRepository creates a new LockRepository.
func NewLockRepository(db generated.DBTX) *LockRepository {
	return &LockRepository{db: db}
}

// Create inserts or replaces a lock for a file.
func (r *LockRepository) Create(ctx context.Context, lock *model.Lock) error {
	if lock == nil {
		return fmt.Errorf("lock is nil")
	}
	q := generated.New(r.db)
	return q.UpsertLock(ctx, generated.UpsertLockParams{
		ID:        uuidToPgtype(lock.ID),
		FileID:    lock.FileID,
		LockID:    lock.LockID,
		ExpiresAt: timestamptzFromTime(lock.ExpiresAt),
		CreatedAt: timestamptzFromTime(lock.CreatedAt),
	})
}

// FindByFileID retrieves the active (non-expired) lock for a file.
func (r *LockRepository) FindByFileID(ctx context.Context, fileID string) (*model.Lock, error) {
	q := generated.New(r.db)
	row, err := q.FindLockByFileID(ctx, fileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &model.Lock{
		ID:        pgTypeToUUID(row.ID),
		FileID:    row.FileID,
		LockID:    row.LockID,
		ExpiresAt: row.ExpiresAt.Time,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

// UpdateLockID atomically replaces the lock ID and expiry (CAS on lock_id).
func (r *LockRepository) UpdateLockID(ctx context.Context, fileID, currentLockID, newLockID string, newExpiry model.Lock) error {
	q := generated.New(r.db)
	rows, err := q.UpdateLockIDAndExpiry(ctx, generated.UpdateLockIDAndExpiryParams{
		FileID:    fileID,
		LockID:    currentLockID,
		LockID_2:  newLockID,
		ExpiresAt: timestamptzFromTime(newExpiry.ExpiresAt),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return port.ErrStaleLock
	}
	return nil
}

// RefreshExpiry extends the lock expiry (CAS on lock_id).
func (r *LockRepository) RefreshExpiry(ctx context.Context, fileID, lockID string, lock *model.Lock) error {
	if lock == nil {
		return fmt.Errorf("lock is nil")
	}
	q := generated.New(r.db)
	rows, err := q.UpdateLockExpiry(ctx, generated.UpdateLockExpiryParams{
		FileID:    fileID,
		LockID:    lockID,
		ExpiresAt: timestamptzFromTime(lock.ExpiresAt),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return port.ErrStaleLock
	}
	return nil
}

// DeleteByFileID removes the lock for a file (CAS on lock_id).
func (r *LockRepository) DeleteByFileID(ctx context.Context, fileID, lockID string) error {
	q := generated.New(r.db)
	rows, err := q.DeleteLockByFileID(ctx, generated.DeleteLockByFileIDParams{
		FileID: fileID,
		LockID: lockID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return port.ErrStaleLock
	}
	return nil
}

// DeleteExpired removes all expired locks and returns the count deleted.
func (r *LockRepository) DeleteExpired(ctx context.Context) (int64, error) {
	q := generated.New(r.db)
	return q.DeleteExpiredLocks(ctx)
}

// Takeover atomically replaces an existing lock (different lockID) with a
// new one — used when an existing lock has lived past MaxLockLifetime and
// is presumed to belong to a zombie session that won't release it. CAS on
// (fileID, oldLockID).
func (r *LockRepository) Takeover(ctx context.Context, fileID, oldLockID, newLockID string, newCreatedAt, newExpiresAt time.Time) error {
	q := generated.New(r.db)
	rows, err := q.TakeoverLock(ctx, generated.TakeoverLockParams{
		FileID:    fileID,
		LockID:    oldLockID,
		LockID_2:  newLockID,
		CreatedAt: timestamptzFromTime(newCreatedAt),
		ExpiresAt: timestamptzFromTime(newExpiresAt),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return port.ErrStaleLock
	}
	return nil
}
