// Package nats implements the authorization adapter using NATS request-reply.
package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const authSubject = "auth.evaluate"

// AuthService implements port.AuthService via the authorization-evaluation-service.
type AuthService struct {
	conn *nats.Conn
}

// NewAuthService creates a new AuthService.
func NewAuthService(conn *nats.Conn) *AuthService {
	return &AuthService{conn: conn}
}

type evaluateRequest struct {
	Pattern string       `json:"pattern"`
	Data    evaluateData `json:"data"`
}

type evaluateData struct {
	AgentID               string `json:"agentId"`
	Privilege             string `json:"privilege"`
	AuthorizationPolicyID string `json:"authorizationPolicyId"`
}

type evaluateResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// CheckPrivilege verifies whether an agent has a privilege on a resource.
func (s *AuthService) CheckPrivilege(ctx context.Context, agentID, privilege, authorizationPolicyID string) (*port.AuthResult, error) {
	req := evaluateRequest{
		Pattern: "evaluate",
		Data: evaluateData{
			AgentID:               agentID,
			Privilege:             privilege,
			AuthorizationPolicyID: authorizationPolicyID,
		},
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal auth request: %w", err)
	}

	msg, err := s.conn.RequestWithContext(ctx, authSubject, payload)
	if err != nil {
		return nil, fmt.Errorf("nats auth request: %w", err)
	}

	var resp evaluateResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal auth response: %w", err)
	}

	return &port.AuthResult{
		Allowed: resp.Allowed,
		Reason:  resp.Reason,
	}, nil
}
