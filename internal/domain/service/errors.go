package service

import "errors"

// Domain errors returned by service methods.
var (
	ErrDocumentNotFound = errors.New("document not found")
	ErrNotAuthorized    = errors.New("not authorized")
	ErrLockMismatch     = errors.New("lock mismatch")
)

// LockConflictError is returned when a lock operation conflicts with an existing lock.
type LockConflictError struct {
	ExistingLockID string
}

func (e *LockConflictError) Error() string {
	if e.ExistingLockID == "" {
		return "lock conflict: file is not locked"
	}
	return "lock conflict: existing lock " + e.ExistingLockID
}
