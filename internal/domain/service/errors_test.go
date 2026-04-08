package service

import "testing"

func TestLockConflictError_WithExistingLock(t *testing.T) {
	err := &LockConflictError{ExistingLockID: "lock-A"}
	got := err.Error()
	if got != "lock conflict: existing lock lock-A" {
		t.Errorf("Error() = %q", got)
	}
}

func TestLockConflictError_NoLock(t *testing.T) {
	err := &LockConflictError{ExistingLockID: ""}
	got := err.Error()
	if got != "lock conflict: file is not locked" {
		t.Errorf("Error() = %q", got)
	}
}
