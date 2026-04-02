// Package authhttp implements the authorization adapter using h2c HTTP to the
// authorization-evaluation-service.
package authhttp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// AuthService implements port.AuthService via h2c HTTP to the
// authorization-evaluation-service.
type AuthService struct {
	baseURL    string
	httpClient *http.Client
}

// NewAuthService creates a new h2c-capable AuthService.
func NewAuthService(baseURL string) *AuthService {
	// h2c transport: HTTP/2 over cleartext (no TLS)
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
	AgentID               string `json:"agentId"`
	Privilege             string `json:"privilege"`
	AuthorizationPolicyID string `json:"authorizationPolicyId"`
}

type evaluateResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// CheckPrivilege verifies whether an agent has a privilege on a resource
// via the authorization-evaluation-service h2c endpoint.
func (s *AuthService) CheckPrivilege(ctx context.Context, agentID, privilege, authorizationPolicyID string) (*port.AuthResult, error) {
	req := evaluateRequest{
		AgentID:               agentID,
		Privilege:             privilege,
		AuthorizationPolicyID: authorizationPolicyID,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal auth request: %w", err)
	}

	url := fmt.Sprintf("%s/internal/auth/evaluate", s.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("create auth request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("h2c auth request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result evaluateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode auth response: %w", err)
	}

	return &port.AuthResult{
		Allowed: result.Allowed,
		Reason:  result.Reason,
	}, nil
}
