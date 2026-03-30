package service

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
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

func TestCleanupService_RunWithErrors(_ *testing.T) {
	// Error-producing repos — tests that cleanup handles errors gracefully
	tokenRepo := &errorTokenRepo{err: context.DeadlineExceeded}
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}

	svc := NewCleanupService(tokenRepo, lockRepo, zap.NewNop())
	svc.interval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Should not panic even with errors
	svc.Start(ctx)
}

type errorTokenRepo struct {
	err error
}

func (e *errorTokenRepo) Create(_ context.Context, _ *model.AccessToken) error { return e.err }
func (e *errorTokenRepo) FindByToken(_ context.Context, _ string) (*model.AccessToken, error) {
	return nil, e.err
}
func (e *errorTokenRepo) DeleteByID(_ context.Context, _ string) error   { return e.err }
func (e *errorTokenRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, e.err }
