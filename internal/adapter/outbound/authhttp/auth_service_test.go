package authhttp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func authTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{Handler: handler, Protocols: protocols, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + listener.Addr().String()
}

func TestCheckPrivilegeH2CConcurrentRequests(t *testing.T) {
	const count = 4
	arrived := make(chan struct{}, count)
	release := make(chan struct{})
	url := authTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload evaluateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.ProtoMajor != 2 || r.URL.Path != "/internal/auth/evaluate" || payload.ActorID != "actor" || payload.AuthorizationPolicyID != "policy" || payload.Privilege != "read" {
			t.Errorf("unexpected authorization request: %s %s %+v", r.Proto, r.URL.Path, payload)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(evaluateResponse{Allowed: true})
	}))
	adapter := NewAuthService(url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			result, err := adapter.CheckPrivilege(ctx, "actor", "read", "policy")
			if err != nil {
				t.Error(err)
				return
			}
			if !result.Allowed {
				t.Error("authorization result was lost")
			}
		})
	}
	for range count {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Error("concurrent h2c requests did not reach authorization peer")
		}
	}
	close(release)
	group.Wait()
}

func TestCheckPrivilegeHonorsContextDeadline(t *testing.T) {
	url := authTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	adapter := NewAuthService(url)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result, err := adapter.CheckPrivilege(ctx, "actor", "read", "policy")
	if err == nil || result != nil || ctx.Err() == nil {
		t.Fatalf("cancelled authorization returned result=%v error=%v context=%v", result, err, ctx.Err())
	}
}
