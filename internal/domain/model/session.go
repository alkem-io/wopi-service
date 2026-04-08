package model

import (
	"time"

	"github.com/google/uuid"
)

// WOPISession tracks an active editing session.
type WOPISession struct {
	ID        uuid.UUID
	FileID    string
	ActorID   string
	TokenID   uuid.UUID
	CreatedAt time.Time
}
