package nats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func startTestServer(t *testing.T) *nats.Conn {
	t.Helper()
	srv := natstest.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL(), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect to test NATS: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestAuthService_CheckPrivilege_Allowed(t *testing.T) {
	nc := startTestServer(t)

	// Start a mock responder on the auth.evaluate subject
	sub, err := nc.Subscribe(authSubject, func(msg *nats.Msg) {
		var req evaluateRequest
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
			return
		}

		if req.Data.AgentID != "actor-1" {
			t.Errorf("agentId = %q, want actor-1", req.Data.AgentID)
		}
		if req.Data.Privilege != "read" {
			t.Errorf("privilege = %q, want read", req.Data.Privilege)
		}
		if req.Data.AuthorizationPolicyID != "policy-1" {
			t.Errorf("authorizationPolicyId = %q, want policy-1", req.Data.AuthorizationPolicyID)
		}

		resp := evaluateResponse{Allowed: true, Reason: "granted by test rule"}
		data, _ := json.Marshal(resp)
		if err := msg.Respond(data); err != nil {
			t.Errorf("respond: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	authSvc := NewAuthService(nc)
	result, err := authSvc.CheckPrivilege(context.Background(), "actor-1", "read", "policy-1")
	if err != nil {
		t.Fatalf("CheckPrivilege error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected Allowed=true")
	}
	if result.Reason != "granted by test rule" {
		t.Errorf("Reason = %q", result.Reason)
	}
}

func TestAuthService_CheckPrivilege_Denied(t *testing.T) {
	nc := startTestServer(t)

	sub, err := nc.Subscribe(authSubject, func(msg *nats.Msg) {
		resp := evaluateResponse{Allowed: false, Reason: "privilege not granted"}
		data, _ := json.Marshal(resp)
		_ = msg.Respond(data)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	authSvc := NewAuthService(nc)
	result, err := authSvc.CheckPrivilege(context.Background(), "actor-1", "delete", "policy-1")
	if err != nil {
		t.Fatalf("CheckPrivilege error: %v", err)
	}
	if result.Allowed {
		t.Error("expected Allowed=false")
	}
}

func TestAuthService_CheckPrivilege_Timeout(t *testing.T) {
	nc := startTestServer(t)
	// No responder — request will timeout

	authSvc := NewAuthService(nc)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := authSvc.CheckPrivilege(ctx, "actor-1", "read", "policy-1")
	if err == nil {
		t.Error("expected error on timeout")
	}
}

func TestAuthService_CheckPrivilege_InvalidResponse(t *testing.T) {
	nc := startTestServer(t)

	sub, err := nc.Subscribe(authSubject, func(msg *nats.Msg) {
		_ = msg.Respond([]byte("not json"))
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	authSvc := NewAuthService(nc)
	_, err = authSvc.CheckPrivilege(context.Background(), "actor-1", "read", "policy-1")
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestNewAuthService(t *testing.T) {
	nc := startTestServer(t)
	svc := NewAuthService(nc)
	if svc == nil {
		t.Error("expected non-nil AuthService")
	}
}
