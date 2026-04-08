package model

import (
	"testing"
	"time"
)

func TestLock_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"future expiry", time.Now().Add(30 * time.Minute), false},
		{"past expiry", time.Now().Add(-1 * time.Minute), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := Lock{ExpiresAt: tt.expiresAt}
			if got := lock.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultLockDuration(t *testing.T) {
	if DefaultLockDuration != 30*time.Minute {
		t.Errorf("DefaultLockDuration = %v, want 30m", DefaultLockDuration)
	}
}
