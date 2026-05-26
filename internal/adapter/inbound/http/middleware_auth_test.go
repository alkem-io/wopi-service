package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// minimal mock for FileService to satisfy TokenService constructor
type stubFileService struct{}

func (s *stubFileService) FindByID(_ context.Context, _ string) (*model.Document, error) {
	return nil, nil
}
func (s *stubFileService) ReadFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubFileService) WriteFile(_ context.Context, _ string, _ io.Reader) (*port.FileWriteResult, error) {
	return nil, nil
}
func (s *stubFileService) FileExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// in-memory token repo for middleware tests
type memTokenRepo struct {
	tokens map[string]*model.AccessToken
}

func (m *memTokenRepo) Create(_ context.Context, token *model.AccessToken) error {
	m.tokens[token.Token] = token
	return nil
}
func (m *memTokenRepo) FindByToken(_ context.Context, v string) (*model.AccessToken, error) {
	return m.tokens[v], nil
}
func (m *memTokenRepo) DeleteByID(_ context.Context, _ string) error   { return nil }
func (m *memTokenRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

type stubAuthSvc struct{}

func (s *stubAuthSvc) CheckPrivilege(_ context.Context, _, _, _ string) (*port.AuthResult, error) {
	return &port.AuthResult{Allowed: true}, nil
}

func makeTokenService(repo *memTokenRepo) *service.TokenService {
	return service.NewTokenService(
		repo, &stubFileService{}, &stubAuthSvc{},
		nil,
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)
}

func TestTokenAuthMiddleware_ValidToken(t *testing.T) {
	repo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	repo.tokens["valid-token"] = &model.AccessToken{
		ID:          uuid.New(),
		Token:       "valid-token",
		FileID:      "file-1",
		ActorID:     "actor-1",
		Permissions: "read,write",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}

	tokenSvc := makeTokenService(repo)
	var capturedToken *model.AccessToken

	handler := TokenAuthMiddleware(tokenSvc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = TokenFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/wopi/files/f1?access_token=valid-token", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if capturedToken == nil {
		t.Fatal("expected token in context")
	}
	if capturedToken.ActorID != "actor-1" {
		t.Errorf("actorID = %q, want actor-1", capturedToken.ActorID)
	}
}

func TestTokenAuthMiddleware_MissingToken(t *testing.T) {
	repo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := makeTokenService(repo)

	handler := TokenAuthMiddleware(tokenSvc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/wopi/files/f1", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestTokenAuthMiddleware_ExpiredToken(t *testing.T) {
	repo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	repo.tokens["expired"] = &model.AccessToken{
		Token:     "expired",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	tokenSvc := makeTokenService(repo)
	handler := TokenAuthMiddleware(tokenSvc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/wopi/files/f1?access_token=expired", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestTokenAuthMiddleware_InvalidToken(t *testing.T) {
	repo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := makeTokenService(repo)

	handler := TokenAuthMiddleware(tokenSvc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/wopi/files/f1?access_token=nonexistent", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestTokenFromContext_Nil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := TokenFromContext(req.Context()); got != nil {
		t.Errorf("TokenFromContext() = %v, want nil", got)
	}
}
