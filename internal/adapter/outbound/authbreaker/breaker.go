// Package authbreaker wraps a port.AuthService with a circuit breaker.
package authbreaker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// BreakerConfig holds circuit breaker settings for the auth service.
type BreakerConfig struct {
	FailureThreshold uint32
	TimeoutSeconds   int
	HalfOpenMax      uint32
}

// Wrap wraps a port.AuthService with a circuit breaker.
func Wrap(inner port.AuthService, cfg BreakerConfig) port.AuthService {
	settings := gobreaker.Settings{
		Name:        "auth-evaluation",
		MaxRequests: cfg.HalfOpenMax,
		Interval:    0, // don't reset counts periodically
		Timeout:     time.Duration(cfg.TimeoutSeconds) * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.FailureThreshold
		},
	}

	cb := gobreaker.NewCircuitBreaker[*port.AuthResult](settings)

	return &breakerAuthService{inner: inner, cb: cb}
}

type breakerAuthService struct {
	inner port.AuthService
	cb    *gobreaker.CircuitBreaker[*port.AuthResult]
}

// CheckPrivilege delegates to the inner auth service, wrapped by a circuit breaker.
func (s *breakerAuthService) CheckPrivilege(ctx context.Context, actorID, privilege, authorizationPolicyID string) (*port.AuthResult, error) {
	result, err := s.cb.Execute(func() (*port.AuthResult, error) {
		return s.inner.CheckPrivilege(ctx, actorID, privilege, authorizationPolicyID)
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return &port.AuthResult{
				Allowed: false,
				Reason:  fmt.Sprintf("auth service circuit breaker %s", s.cb.State().String()),
			}, err
		}
		return nil, err
	}
	return result, nil
}
