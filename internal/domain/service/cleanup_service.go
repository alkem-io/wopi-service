package service

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// CleanupService periodically removes expired tokens and locks.
type CleanupService struct {
	tokenRepo port.TokenRepository
	lockRepo  port.LockRepository
	interval  time.Duration
	logger    *zap.Logger
}

// NewCleanupService creates a new CleanupService.
func NewCleanupService(
	tokenRepo port.TokenRepository,
	lockRepo port.LockRepository,
	logger *zap.Logger,
) *CleanupService {
	return &CleanupService{
		tokenRepo: tokenRepo,
		lockRepo:  lockRepo,
		interval:  15 * time.Minute,
		logger:    logger,
	}
}

// Start begins the periodic cleanup loop. It blocks until the context is cancelled.
func (s *CleanupService) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("cleanup service started", zap.Duration("interval", s.interval))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("cleanup service stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

func (s *CleanupService) run(ctx context.Context) {
	tokensDeleted, err := s.tokenRepo.DeleteExpired(ctx)
	if err != nil {
		s.logger.Error("failed to clean expired tokens", zap.Error(err))
	} else if tokensDeleted > 0 {
		s.logger.Info("cleaned expired tokens", zap.Int64("count", tokensDeleted))
	}

	locksDeleted, err := s.lockRepo.DeleteExpired(ctx)
	if err != nil {
		s.logger.Error("failed to clean expired locks", zap.Error(err))
	} else if locksDeleted > 0 {
		s.logger.Info("cleaned expired locks", zap.Int64("count", locksDeleted))
	}
}
