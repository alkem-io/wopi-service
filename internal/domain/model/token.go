// Package model contains domain entities and value objects.
package model

import (
	"time"

	"github.com/google/uuid"
)

// AccessToken represents an opaque WOPI access token stored in the database.
type AccessToken struct {
	ID          uuid.UUID
	Token       string
	FileID      string
	ActorID     string
	Permissions string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// IsExpired returns true if the token has passed its expiry time.
func (t AccessToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// HasPermission checks whether the token grants the given permission.
func (t AccessToken) HasPermission(perm string) bool {
	for i := 0; i < len(t.Permissions); {
		j := i
		for j < len(t.Permissions) && t.Permissions[j] != ',' {
			j++
		}
		if t.Permissions[i:j] == perm {
			return true
		}
		i = j + 1
	}
	return false
}
