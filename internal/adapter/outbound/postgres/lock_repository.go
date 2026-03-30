package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres/generated"
	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// LockRepository implements port.LockRepository using PostgreSQL.
type LockRepository struct {
	pool *pgxpool.Pool
}

// NewLockRepository creates a new LockRepository.
func NewLockRepository(pool *pgxpool.Pool) *LockRepository {
	return &LockRepository{pool: pool}
}

// Create inserts or replaces a lock for a file.
func (r *LockRepository) Create(ctx context.Context, lock *model.Lock) error {
	q := generated.New(r.pool)
	return q.UpsertLock(ctx, generated.UpsertLockParams{
		ID:        uuidToPgtype(lock.ID),
		FileID:    lock.FileID,
		LockID:    lock.LockID,
		ExpiresAt: timestamptzFromTime(lock.ExpiresAt),
		CreatedAt: timestamptzFromTime(lock.CreatedAt),
	})
}

// FindByFileID retrieves the active lock for a file. Returns nil if none.
func (r *LockRepository) FindByFileID(ctx context.Context, fileID string) (*model.Lock, error) {
	q := generated.New(r.pool)
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

// UpdateLockID atomically replaces the lock ID and expiry for a file.
func (r *LockRepository) UpdateLockID(ctx context.Context, fileID, newLockID string, lock model.Lock) error {
	q := generated.New(r.pool)
	return q.UpdateLockIDAndExpiry(ctx, generated.UpdateLockIDAndExpiryParams{
		FileID:    fileID,
		LockID:    newLockID,
		ExpiresAt: timestamptzFromTime(lock.ExpiresAt),
	})
}

// RefreshExpiry extends the lock expiry for a file.
func (r *LockRepository) RefreshExpiry(ctx context.Context, fileID string, lock *model.Lock) error {
	q := generated.New(r.pool)
	return q.UpdateLockExpiry(ctx, generated.UpdateLockExpiryParams{
		FileID:    fileID,
		ExpiresAt: timestamptzFromTime(lock.ExpiresAt),
	})
}

// DeleteByFileID removes the lock for a file.
func (r *LockRepository) DeleteByFileID(ctx context.Context, fileID string) error {
	q := generated.New(r.pool)
	return q.DeleteLockByFileID(ctx, fileID)
}

// DeleteExpired removes all expired locks and returns the count deleted.
func (r *LockRepository) DeleteExpired(ctx context.Context) (int64, error) {
	q := generated.New(r.pool)
	return q.DeleteExpiredLocks(ctx)
}
