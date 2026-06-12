package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v5"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

func TestLockRepository_Create(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)
	lock := &model.Lock{
		ID:        uuid.New(),
		FileID:    "file-1",
		LockID:    "lock-1",
		ExpiresAt: time.Now().Add(30 * time.Minute),
		CreatedAt: time.Now(),
	}

	mock.ExpectExec("INSERT INTO locks").
		WithArgs(pgxmock.AnyArg(), lock.FileID, lock.LockID, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(context.Background(), lock)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_FindByFileID_Found(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)
	lockID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"id", "file_id", "lock_id", "expires_at", "created_at"}).
		AddRow(
			pgtype.UUID{Bytes: lockID, Valid: true},
			"file-1",
			"lock-A",
			pgtype.Timestamptz{Time: now.Add(30 * time.Minute), Valid: true},
			pgtype.Timestamptz{Time: now, Valid: true},
		)

	mock.ExpectQuery("SELECT .+ FROM locks WHERE file_id").
		WithArgs("file-1").
		WillReturnRows(rows)

	result, err := repo.FindByFileID(context.Background(), "file-1")
	if err != nil {
		t.Fatalf("FindByFileID error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.LockID != "lock-A" {
		t.Errorf("LockID = %q", result.LockID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_FindByFileID_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)

	mock.ExpectQuery("SELECT .+ FROM locks WHERE file_id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)

	result, err := repo.FindByFileID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result != nil {
		t.Error("expected nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_RefreshExpiry(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)
	lock := &model.Lock{ExpiresAt: time.Now().Add(30 * time.Minute)}

	mock.ExpectExec("UPDATE locks SET expires_at").
		WithArgs("file-1", "lock-1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = repo.RefreshExpiry(context.Background(), "file-1", "lock-1", lock)
	if err != nil {
		t.Fatalf("RefreshExpiry error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_UpdateLockID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)
	lock := model.Lock{ExpiresAt: time.Now().Add(30 * time.Minute)}

	mock.ExpectExec("UPDATE locks SET lock_id").
		WithArgs("file-1", "old-lock", "new-lock", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = repo.UpdateLockID(context.Background(), "file-1", "old-lock", "new-lock", lock)
	if err != nil {
		t.Fatalf("UpdateLockID error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_DeleteByFileID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)

	mock.ExpectExec("DELETE FROM locks WHERE file_id").
		WithArgs("file-1", "lock-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = repo.DeleteByFileID(context.Background(), "file-1", "lock-1")
	if err != nil {
		t.Fatalf("DeleteByFileID error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLockRepository_DeleteExpired(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewLockRepository(mock)

	mock.ExpectExec("DELETE FROM locks WHERE expires_at").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))

	count, err := repo.DeleteExpired(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpired error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
