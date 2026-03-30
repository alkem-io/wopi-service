package model

import (
	"time"

	"github.com/google/uuid"
)

// DefaultLockDuration is the default WOPI lock expiry (30 minutes).
const DefaultLockDuration = 30 * time.Minute

// Lock represents an active WOPI edit lock on a file.
type Lock struct {
	ID        uuid.UUID
	FileID    string
	LockID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// IsExpired returns true if the lock has passed its expiry time.
func (l Lock) IsExpired() bool {
	return time.Now().After(l.ExpiresAt)
}
