package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

func TestSessionRepository_Create(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewSessionRepository(mock)
	session := &model.WOPISession{
		ID:        uuid.New(),
		FileID:    "file-1",
		ActorID:   "actor-1",
		TokenID:   uuid.New(),
		CreatedAt: time.Now(),
	}

	mock.ExpectExec("INSERT INTO wopi_sessions").
		WithArgs(pgxmock.AnyArg(), session.FileID, session.ActorID, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(context.Background(), session)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSessionRepository_FindByFileID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewSessionRepository(mock)
	sid := uuid.New()
	tid := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"id", "file_id", "actor_id", "token_id", "created_at"}).
		AddRow(
			pgtype.UUID{Bytes: sid, Valid: true},
			"file-1",
			"actor-1",
			pgtype.UUID{Bytes: tid, Valid: true},
			pgtype.Timestamptz{Time: now, Valid: true},
		)

	mock.ExpectQuery("SELECT .+ FROM wopi_sessions WHERE file_id").
		WithArgs("file-1").
		WillReturnRows(rows)

	sessions, err := repo.FindByFileID(context.Background(), "file-1")
	if err != nil {
		t.Fatalf("FindByFileID error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ActorID != "actor-1" {
		t.Errorf("ActorID = %q", sessions[0].ActorID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSessionRepository_DeleteByTokenID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewSessionRepository(mock)
	tokenID := uuid.New()

	mock.ExpectExec("DELETE FROM wopi_sessions WHERE token_id").
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = repo.DeleteByTokenID(context.Background(), tokenID.String())
	if err != nil {
		t.Fatalf("DeleteByTokenID error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSessionRepository_DeleteByTokenID_InvalidUUID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewSessionRepository(mock)
	err = repo.DeleteByTokenID(context.Background(), "not-a-uuid")
	if err == nil {
		t.Error("expected error for invalid UUID")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB call on invalid UUID: %v", err)
	}
}
