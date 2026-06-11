package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// File kept as `middleware_jwt_test.go` for git-history continuity. The
// JWT-based middleware was replaced by `ActorHeaderMiddleware` when identity
// extraction moved to the Traefik `alkemio-resolve` forwardAuth gateway.

func TestActorHeaderMiddleware_ValidHeader(t *testing.T) {
	var captured string
	handler := ActorHeaderMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = ActorIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/wopi/token", nil)
	req.Header.Set(HeaderActorID, "actor-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if captured != "actor-123" {
		t.Fatalf("expected actor id 'actor-123', got %q", captured)
	}
}

func TestActorHeaderMiddleware_MissingHeader(t *testing.T) {
	handler := ActorHeaderMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("next handler must not be invoked when header is missing")
	}))

	req := httptest.NewRequest(http.MethodPost, "/wopi/token", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestActorHeaderMiddleware_EmptyHeader(t *testing.T) {
	handler := ActorHeaderMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("next handler must not be invoked when header is empty")
	}))

	req := httptest.NewRequest(http.MethodPost, "/wopi/token", nil)
	req.Header.Set(HeaderActorID, "")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestActorHeaderMiddleware_WhitespaceOnlyHeader(t *testing.T) {
	handler := ActorHeaderMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("next handler must not be invoked when header is whitespace-only")
	}))

	req := httptest.NewRequest(http.MethodPost, "/wopi/token", nil)
	req.Header.Set(HeaderActorID, "   ")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestActorNameFromContext_DefaultsToEmpty(t *testing.T) {
	got := ActorNameFromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	if got != "" {
		t.Fatalf("expected '' from new context, got %q", got)
	}
}
