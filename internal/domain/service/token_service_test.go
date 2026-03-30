package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// --- In-memory mocks ---

type mockTokenRepo struct {
	tokens map[string]*model.AccessToken
}

func newMockTokenRepo() *mockTokenRepo {
	return &mockTokenRepo{tokens: make(map[string]*model.AccessToken)}
}

func (m *mockTokenRepo) Create(_ context.Context, token *model.AccessToken) error {
	m.tokens[token.Token] = token
	return nil
}

func (m *mockTokenRepo) FindByToken(_ context.Context, tokenValue string) (*model.AccessToken, error) {
	return m.tokens[tokenValue], nil
}

func (m *mockTokenRepo) DeleteByID(_ context.Context, id string) error {
	for k, v := range m.tokens {
		if v.ID.String() == id {
			delete(m.tokens, k)
		}
	}
	return nil
}

func (m *mockTokenRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

type mockDocRepo struct {
	docs map[string]*model.Document
}

func newMockDocRepo() *mockDocRepo {
	return &mockDocRepo{docs: make(map[string]*model.Document)}
}

func (m *mockDocRepo) FindByID(_ context.Context, id string) (*model.Document, error) {
	return m.docs[id], nil
}

type mockAuthSvc struct {
	results map[string]bool // key: "agentId:privilege"
}

func newMockAuthSvc() *mockAuthSvc {
	return &mockAuthSvc{results: make(map[string]bool)}
}

func (m *mockAuthSvc) CheckPrivilege(_ context.Context, agentID, privilege, _ string) (*port.AuthResult, error) {
	key := agentID + ":" + privilege
	allowed := m.results[key]
	return &port.AuthResult{Allowed: allowed, Reason: "mock"}, nil
}

type mockSessionRepo struct {
	sessions []*model.WOPISession
}

func (m *mockSessionRepo) Create(_ context.Context, s *model.WOPISession) error {
	m.sessions = append(m.sessions, s)
	return nil
}

func (m *mockSessionRepo) FindByFileID(_ context.Context, _ string) ([]model.WOPISession, error) {
	return nil, nil
}

func (m *mockSessionRepo) DeleteByTokenID(_ context.Context, _ string) error { return nil }

// --- Tests ---

func TestIssueToken_Success_ReadWrite(t *testing.T) {
	docID := uuid.New().String()
	actorID := uuid.New().String()

	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{
		ID:                    docID,
		ExternalID:            "abc123",
		DisplayName:           "test.docx",
		MimeType:              "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Size:                  1024,
		AuthorizationPolicyID: uuid.New().String(),
	}

	authSvc := newMockAuthSvc()
	authSvc.results[actorID+":read"] = true
	authSvc.results[actorID+":update-content"] = true

	svc := NewTokenService(
		newMockTokenRepo(), docRepo, authSvc, &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	result, err := svc.IssueToken(context.Background(), actorID, docID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken == "" {
		t.Error("expected non-empty access token")
	}
	if result.WOPISrc == "" {
		t.Error("expected non-empty WOPISrc")
	}
	if result.TTL == 0 {
		t.Error("expected non-zero TTL")
	}
}

func TestIssueToken_Success_ReadOnly(t *testing.T) {
	docID := uuid.New().String()
	actorID := uuid.New().String()

	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{
		ID:                    docID,
		AuthorizationPolicyID: uuid.New().String(),
	}

	authSvc := newMockAuthSvc()
	authSvc.results[actorID+":read"] = true
	// update-content not granted

	tokenRepo := newMockTokenRepo()
	svc := NewTokenService(
		tokenRepo, docRepo, authSvc, &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	result, err := svc.IssueToken(context.Background(), actorID, docID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored := tokenRepo.tokens[result.AccessToken]
	if stored == nil {
		t.Fatal("token not stored")
	}
	if stored.Permissions != "read" {
		t.Errorf("expected read-only permissions, got %q", stored.Permissions)
	}
}

func TestIssueToken_DocumentNotFound(t *testing.T) {
	svc := NewTokenService(
		newMockTokenRepo(), newMockDocRepo(), newMockAuthSvc(), &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	_, err := svc.IssueToken(context.Background(), "actor", "nonexistent")
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("expected ErrDocumentNotFound, got %v", err)
	}
}

func TestIssueToken_NotAuthorized(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{
		ID:                    docID,
		AuthorizationPolicyID: uuid.New().String(),
	}

	authSvc := newMockAuthSvc()
	// read not granted

	svc := NewTokenService(
		newMockTokenRepo(), docRepo, authSvc, &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	_, err := svc.IssueToken(context.Background(), "actor", docID)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("expected ErrNotAuthorized, got %v", err)
	}
}

func TestValidateToken_Valid(t *testing.T) {
	tokenRepo := newMockTokenRepo()
	token := &model.AccessToken{
		ID:          uuid.New(),
		Token:       "valid-token",
		FileID:      "file-1",
		ActorID:     "actor-1",
		Permissions: "read,write",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		CreatedAt:   time.Now(),
	}
	tokenRepo.tokens["valid-token"] = token

	svc := NewTokenService(
		tokenRepo, newMockDocRepo(), newMockAuthSvc(), &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	result, err := svc.ValidateToken(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil token")
	}
	if result.ActorID != "actor-1" {
		t.Errorf("expected actor-1, got %s", result.ActorID)
	}
}

func TestValidateToken_Expired(t *testing.T) {
	tokenRepo := newMockTokenRepo()
	tokenRepo.tokens["expired-token"] = &model.AccessToken{
		Token:     "expired-token",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	svc := NewTokenService(
		tokenRepo, newMockDocRepo(), newMockAuthSvc(), &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	result, err := svc.ValidateToken(context.Background(), "expired-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for expired token")
	}
}

func TestValidateToken_NotFound(t *testing.T) {
	svc := NewTokenService(
		newMockTokenRepo(), newMockDocRepo(), newMockAuthSvc(), &mockSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)

	result, err := svc.ValidateToken(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil for missing token")
	}
}

func TestGenerateURLSafeToken_Format(t *testing.T) {
	token, err := generateURLSafeToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(token) == 0 {
		t.Error("expected non-empty token")
	}
	// URL-safe base64 uses only A-Z, a-z, 0-9, -, _
	for _, c := range token {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '-' && c != '_' {
			t.Errorf("token contains non-URL-safe character: %c", c)
		}
	}
}
