// Package service implements domain use cases for the WOPI service.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const defaultTokenTTL = 8 * time.Hour

// TokenService handles WOPI access token generation, validation, and issuance.
type TokenService struct {
	tokenRepo   port.TokenRepository
	docRepo     port.DocumentRepository
	authSvc     port.AuthService
	sessionRepo port.SessionRepository
	secret      string
	baseURL     string
	logger      *zap.Logger
}

// NewTokenService creates a new TokenService.
func NewTokenService(
	tokenRepo port.TokenRepository,
	docRepo port.DocumentRepository,
	authSvc port.AuthService,
	sessionRepo port.SessionRepository,
	secret string,
	baseURL string,
	logger *zap.Logger,
) *TokenService {
	return &TokenService{
		tokenRepo:   tokenRepo,
		docRepo:     docRepo,
		authSvc:     authSvc,
		sessionRepo: sessionRepo,
		secret:      secret,
		baseURL:     baseURL,
		logger:      logger,
	}
}

// TokenIssuanceResult holds the response for a token issuance request.
type TokenIssuanceResult struct {
	AccessToken string
	TTL         int64  // UNIX timestamp in milliseconds
	WOPISrc     string // Full WOPI file URL
}

// IssueToken authenticates and authorizes an actor, then creates a WOPI access token.
func (s *TokenService) IssueToken(ctx context.Context, actorID, documentID string) (*TokenIssuanceResult, error) {
	doc, err := s.docRepo.FindByID(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("lookup document: %w", err)
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	// Check read permission
	readResult, err := s.authSvc.CheckPrivilege(ctx, actorID, "read", doc.AuthorizationPolicyID)
	if err != nil {
		return nil, fmt.Errorf("check read privilege: %w", err)
	}
	if !readResult.Allowed {
		return nil, ErrNotAuthorized
	}

	// Check write permission (optional — determines token permissions)
	permissions := "read"
	writeResult, err := s.authSvc.CheckPrivilege(ctx, actorID, "update-content", doc.AuthorizationPolicyID)
	if err != nil {
		s.logger.Warn("failed to check write privilege, defaulting to read-only",
			zap.Error(err), zap.String("actorId", actorID))
	} else if writeResult.Allowed {
		permissions = "read,write"
	}

	// Generate token
	token, err := generateURLSafeToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(defaultTokenTTL)

	accessToken := &model.AccessToken{
		ID:          uuid.New(),
		Token:       token,
		FileID:      documentID,
		ActorID:     actorID,
		Permissions: permissions,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
	}

	if err := s.tokenRepo.Create(ctx, accessToken); err != nil {
		return nil, fmt.Errorf("store token: %w", err)
	}

	// Create session
	session := &model.WOPISession{
		ID:        uuid.New(),
		FileID:    documentID,
		ActorID:   actorID,
		TokenID:   accessToken.ID,
		CreatedAt: now,
	}
	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &TokenIssuanceResult{
		AccessToken: token,
		TTL:         expiresAt.UnixMilli(),
		WOPISrc:     fmt.Sprintf("%s/wopi/files/%s", s.baseURL, documentID),
	}, nil
}

// ValidateToken looks up and validates an opaque access token.
// Returns nil if the token is not found or expired.
func (s *TokenService) ValidateToken(ctx context.Context, tokenValue string) (*model.AccessToken, error) {
	token, err := s.tokenRepo.FindByToken(ctx, tokenValue)
	if err != nil {
		return nil, fmt.Errorf("find token: %w", err)
	}
	if token == nil {
		return nil, nil
	}
	if token.IsExpired() {
		return nil, nil
	}
	return token, nil
}

func generateURLSafeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
