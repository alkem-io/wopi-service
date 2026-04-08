// Package authhttp implements the authorization adapter using h2c HTTP to the
// authorization-evaluation-service.
package authhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const (
	maxRetries    = 3
	retryBaseWait = 50 * time.Millisecond
)

// AuthService implements port.AuthService via h2c HTTP to the
// authorization-evaluation-service.
type AuthService struct {
	baseURL    string
	httpClient *http.Client
}

// NewAuthService creates a new h2c-capable AuthService.
func NewAuthService(baseURL string) *AuthService {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}

	return &AuthService{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

type evaluateRequest struct {
	ActorID               string `json:"actorId"`
	Privilege             string `json:"privilege"`
	AuthorizationPolicyID string `json:"authorizationPolicyId"`
}

type evaluateResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// CheckPrivilege verifies whether an agent has a privilege on a resource
// via the authorization-evaluation-service h2c endpoint. Retries on
// transient connection errors (connection reset, refused, EOF).
func (s *AuthService) CheckPrivilege(ctx context.Context, actorID, privilege, authorizationPolicyID string) (*port.AuthResult, error) {
	req := evaluateRequest{
		ActorID:               actorID,
		Privilege:             privilege,
		AuthorizationPolicyID: authorizationPolicyID,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal auth request: %w", err)
	}

	var lastErr error
	for attempt := range maxRetries {
		result, err := s.doRequest(ctx, payload)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return nil, fmt.Errorf("auth request cancelled: %w", lastErr)
		}

		// Wait before retry with exponential backoff
		if attempt < maxRetries-1 {
			wait := retryBaseWait << uint(attempt)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, fmt.Errorf("auth request cancelled: %w", lastErr)
			}
		}
	}

	return nil, fmt.Errorf("auth request failed after %d attempts: %w", maxRetries, lastErr)
}

func (s *AuthService) doRequest(ctx context.Context, payload []byte) (*port.AuthResult, error) {
	url := fmt.Sprintf("%s/internal/auth/evaluate", s.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create auth request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err // transient — will be retried
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return nil, fmt.Errorf("auth service unavailable (503)")
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest:
		return nil, fmt.Errorf("auth service returned unexpected status %d", resp.StatusCode)
	}

	var result evaluateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode auth response: %w", err)
	}

	return &port.AuthResult{
		Allowed: result.Allowed,
		Reason:  result.Reason,
	}, nil
}
