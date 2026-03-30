package service

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestCleanupService_Run(_ *testing.T) {
	tokenRepo := newMockTokenRepo()
	lockRepo := newMockLockRepo()

	svc := NewCleanupService(tokenRepo, lockRepo, zap.NewNop())
	// Shorten interval for test
	svc.interval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run should return when context is cancelled
	svc.Start(ctx)
	// If we reach here without hanging, the test passes
}

func TestNewCleanupService(t *testing.T) {
	svc := NewCleanupService(newMockTokenRepo(), newMockLockRepo(), zap.NewNop())
	if svc.interval != 15*time.Minute {
		t.Errorf("interval = %v, want 15m", svc.interval)
	}
}
