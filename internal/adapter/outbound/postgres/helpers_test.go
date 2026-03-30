package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTimestamptzFromTime(t *testing.T) {
	now := time.Now()
	result := timestamptzFromTime(now)
	if !result.Valid {
		t.Error("expected Valid=true")
	}
	if !result.Time.Equal(now) {
		t.Errorf("time mismatch: got %v, want %v", result.Time, now)
	}
}

func TestPgTypeToUUID(t *testing.T) {
	original := uuid.New()
	pgUUID := uuidToPgtype(original)
	back := pgTypeToUUID(pgUUID)
	if back != original {
		t.Errorf("round-trip failed: got %v, want %v", back, original)
	}
}

func TestUuidToPgtype(t *testing.T) {
	id := uuid.New()
	result := uuidToPgtype(id)
	if !result.Valid {
		t.Error("expected Valid=true")
	}
	if result.Bytes != id {
		t.Error("bytes mismatch")
	}
}

func TestParseUUID_Valid(t *testing.T) {
	id := uuid.New()
	result, err := parseUUID(id.String())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Valid {
		t.Error("expected Valid=true")
	}
	if uuid.UUID(result.Bytes) != id {
		t.Error("UUID mismatch")
	}
}

func TestParseUUID_Invalid(t *testing.T) {
	_, err := parseUUID("not-a-uuid")
	if err == nil {
		t.Error("expected error for invalid UUID")
	}
}
