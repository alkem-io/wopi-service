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
	// Mark stale but keep the snapshot for fallback if refresh fails.
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()

	return s.refresh(ctx)
}

// ErrNoDiscoveryData is returned when the discovery cache is empty.
var ErrNoDiscoveryData = fmt.Errorf("no discovery data available")

// ErrUnsupportedExtension is returned when no editor action matches the extension.
var ErrUnsupportedExtension = fmt.Errorf("no editor action for extension")

// FindActionByExtension looks up a cached discovery action for the given file extension.
// When preferEdit is true, the "edit" action is preferred; otherwise "view" is preferred.
// Falls back to whichever action is available.
func (s *DiscoveryService) FindActionByExtension(ext string, preferEdit bool) (*port.DiscoveryAction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cached == nil {
		return nil, ErrNoDiscoveryData
	}

	preferred := "edit"
	fallback := "view"
	if !preferEdit {
		preferred = "view"
		fallback = "edit"
	}

	var fallbackAction *port.DiscoveryAction
	for i := range s.cached.Actions {
		a := &s.cached.Actions[i]
		if a.Ext != ext {
			continue
		}
		if a.Name == preferred {
			return a, nil
		}
		if a.Name == fallback {
			fallbackAction = a
		}
	}

	if fallbackAction != nil {
		return fallbackAction, nil
	}

	return nil, ErrUnsupportedExtension
}

// GetProofKeys returns the cached proof keys, or nil if no discovery data is available.
func (s *DiscoveryService) GetProofKeys() *port.ProofKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cached == nil {
		return nil
	}
	pk := s.cached.ProofKey
	return &pk
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
