package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const defaultDiscoveryCacheTTL = 12 * time.Hour

// DiscoveryService caches WOPI discovery data from Collabora.
type DiscoveryService struct {
	client   port.DiscoveryClient
	logger   *zap.Logger
	mu       sync.RWMutex
	cached   *port.DiscoveryData
	cachedAt time.Time
	cacheTTL time.Duration
}

// NewDiscoveryService creates a new DiscoveryService.
func NewDiscoveryService(client port.DiscoveryClient, logger *zap.Logger) *DiscoveryService {
	return &DiscoveryService{
		client:   client,
		logger:   logger,
		cacheTTL: defaultDiscoveryCacheTTL,
	}
}

// GetDiscovery returns cached discovery data, fetching from Collabora if the cache is stale.
func (s *DiscoveryService) GetDiscovery(ctx context.Context) (*port.DiscoveryData, error) {
	s.mu.RLock()
	if s.cached != nil && time.Since(s.cachedAt) < s.cacheTTL {
		data := s.cached
		s.mu.RUnlock()
		return data, nil
	}
	s.mu.RUnlock()

	return s.refresh(ctx)
}

// InvalidateAndRefresh clears the cache and fetches fresh discovery data.
// Called when proof key validation fails (keys may have rotated).
func (s *DiscoveryService) InvalidateAndRefresh(ctx context.Context) (*port.DiscoveryData, error) {
	s.mu.Lock()
	s.cached = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()

	return s.refresh(ctx)
}

func (s *DiscoveryService) refresh(ctx context.Context) (*port.DiscoveryData, error) {
	data, err := s.client.FetchDiscovery(ctx)
	if err != nil {
		// Return stale cache if available
		s.mu.RLock()
		stale := s.cached
		s.mu.RUnlock()
		if stale != nil {
			s.logger.Warn("using stale discovery cache", zap.Error(err))
			return stale, nil
		}
		return nil, fmt.Errorf("fetch discovery: %w", err)
	}

	s.mu.Lock()
	s.cached = data
	s.cachedAt = time.Now()
	s.mu.Unlock()

	return data, nil
}
