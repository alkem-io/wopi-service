package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v5"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

func TestPreviewCacheRepository_FindBySourceID_Found(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewPreviewCacheRepository(mock)
	now := time.Now().UTC().Truncate(time.Microsecond)

	rows := pgxmock.NewRows([]string{"source_file_id", "preview_file_id", "source_updated_date"}).
		AddRow("source-1", "preview-1", pgtype.Timestamptz{Time: now, Valid: true})
	mock.ExpectQuery("SELECT .+ FROM document_preview_cache WHERE source_file_id").
		WithArgs("source-1").
		WillReturnRows(rows)

	result, err := repo.FindBySourceID(context.Background(), "source-1")
	if err != nil {
		t.Fatalf("FindBySourceID error: %v", err)
	}
	if result == nil || result.PreviewFileID != "preview-1" || !result.SourceUpdatedDate.Equal(now) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPreviewCacheRepository_FindBySourceID_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewPreviewCacheRepository(mock)
	mock.ExpectQuery("SELECT .+ FROM document_preview_cache WHERE source_file_id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)

	result, err := repo.FindBySourceID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("expected nil error for no rows, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result, got %+v", result)
	}
}

func TestPreviewCacheRepository_Upsert_WritesBothFieldsTogether(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	repo := NewPreviewCacheRepository(mock)
	entry := model.PreviewCacheEntry{
		SourceFileID:      "source-1",
		PreviewFileID:     "preview-2",
		SourceUpdatedDate: time.Now(),
	}

	// A single INSERT ... ON CONFLICT DO UPDATE statement carrying both
	// columns proves preview_file_id and source_updated_date are always
	// committed together — never as two separate statements.
	mock.ExpectExec("INSERT INTO document_preview_cache").
		WithArgs(entry.SourceFileID, entry.PreviewFileID, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Upsert(context.Background(), entry); err != nil {
		t.Fatalf("Upsert error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
