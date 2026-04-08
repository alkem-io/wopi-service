package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

func TestTokenRepository_Create(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)
	token := &model.AccessToken{
		ID:          uuid.New(),
		Token:       "test-token",
		FileID:      "file-1",
		ActorID:     "actor-1",
		Permissions: "read,write",
		ExpiresAt:   time.Now().Add(8 * time.Hour),
		CreatedAt:   time.Now(),
	}

	mock.ExpectExec("INSERT INTO access_tokens").
		WithArgs(
			pgxmock.AnyArg(), // id
			token.Token,
			token.FileID,
			token.ActorID,
			token.Permissions,
			pgxmock.AnyArg(), // expires_at
			pgxmock.AnyArg(), // created_at
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(context.Background(), token)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestTokenRepository_FindByToken_Found(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)
	tokenID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"id", "token", "file_id", "actor_id", "permissions", "expires_at", "created_at"}).
		AddRow(
			pgtype.UUID{Bytes: tokenID, Valid: true},
			"found-token",
			"file-1",
			"actor-1",
			"read",
			pgtype.Timestamptz{Time: now.Add(8 * time.Hour), Valid: true},
			pgtype.Timestamptz{Time: now, Valid: true},
		)

	mock.ExpectQuery("SELECT .+ FROM access_tokens WHERE token").
		WithArgs("found-token").
		WillReturnRows(rows)

	result, err := repo.FindByToken(context.Background(), "found-token")
	if err != nil {
		t.Fatalf("FindByToken error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Token != "found-token" {
		t.Errorf("Token = %q", result.Token)
	}
	if result.ActorID != "actor-1" {
		t.Errorf("ActorID = %q", result.ActorID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestTokenRepository_FindByToken_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)

	mock.ExpectQuery("SELECT .+ FROM access_tokens WHERE token").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)

	result, err := repo.FindByToken(context.Background(), "missing")
	if err != nil {
		t.Fatalf("FindByToken error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for not found")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestTokenRepository_DeleteByID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)
	id := uuid.New()

	mock.ExpectExec("DELETE FROM access_tokens WHERE id").
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = repo.DeleteByID(context.Background(), id.String())
	if err != nil {
		t.Fatalf("DeleteByID error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestTokenRepository_DeleteByID_InvalidUUID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)
	err = repo.DeleteByID(context.Background(), "not-a-uuid")
	if err == nil {
		t.Error("expected error for invalid UUID")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB call on invalid UUID: %v", err)
	}
}

func TestTokenRepository_DeleteExpired(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewTokenRepository(mock)

	mock.ExpectExec("DELETE FROM access_tokens WHERE expires_at").
		WillReturnResult(pgxmock.NewResult("DELETE", 5))

	count, err := repo.DeleteExpired(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpired error: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
