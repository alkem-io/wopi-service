// Package service implements domain use cases for the WOPI service.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const defaultTokenTTL = 8 * time.Hour

// TokenService handles WOPI access token generation, validation, and issuance.
type TokenService struct {
	tokenRepo    port.TokenRepository
	fileSvc      port.FileService
	authSvc      port.AuthService
	sessionRepo  port.SessionRepository
	discoverySvc *DiscoveryService
	secret       string
	baseURL      string
	logger       *zap.Logger
}

// NewTokenService creates a new TokenService.
func NewTokenService(
	tokenRepo port.TokenRepository,
	fileSvc port.FileService,
	authSvc port.AuthService,
	sessionRepo port.SessionRepository,
	discoverySvc *DiscoveryService,
	secret string,
	baseURL string,
	logger *zap.Logger,
) *TokenService {
	return &TokenService{
		tokenRepo:    tokenRepo,
		fileSvc:      fileSvc,
		authSvc:      authSvc,
		sessionRepo:  sessionRepo,
		discoverySvc: discoverySvc,
		secret:       secret,
		baseURL:      baseURL,
		logger:       logger,
	}
}

// TokenIssuanceResult holds the response for a token issuance request.
type TokenIssuanceResult struct {
	AccessToken string
	TTL         int64  // UNIX timestamp in milliseconds
	WOPISrc     string // Full WOPI file URL
	EditorURL   string // Ready-to-use Collabora editor URL
}

// IssueToken authenticates and authorizes an actor, then creates a WOPI access token.
func (s *TokenService) IssueToken(ctx context.Context, actorID, documentID string) (*TokenIssuanceResult, error) {
	doc, err := s.fileSvc.FindByID(ctx, documentID)
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
	wopiSrc := fmt.Sprintf("%s/wopi/files/%s", s.baseURL, documentID)
	ttlMs := expiresAt.UnixMilli()

	// Resolve editor URL BEFORE persisting token/session to avoid orphaned
	// rows if the MIME type is unsupported or discovery is unavailable.
	canWrite := permissions == "read,write"
	editorURL, err := s.resolveEditorURL(doc.MimeType, wopiSrc, token, ttlMs, canWrite)
	if err != nil {
		return nil, fmt.Errorf("resolve editor URL: %w", err)
	}

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
		TTL:         ttlMs,
		WOPISrc:     wopiSrc,
		EditorURL:   editorURL,
	}, nil
}

// resolveEditorURL builds the Collabora editor URL for a document.
func (s *TokenService) resolveEditorURL(mimeType, wopiSrc, accessToken string, ttlMs int64, canWrite bool) (string, error) {
	ext, err := model.ExtensionForMIME(mimeType)
	if err != nil {
		return "", err
	}

	action, err := s.discoverySvc.FindActionByExtension(ext, canWrite)
	if err != nil {
		return "", err
	}

	return buildEditorURL(action.URLSrc, s.baseURL, wopiSrc, accessToken, ttlMs), nil
}

// buildEditorURL constructs the final editor URL by replacing the Collabora
// internal host with WOPI_BASE_URL and appending WOPI parameters.
func buildEditorURL(urlSrc, baseURL, wopiSrc, accessToken string, ttlMs int64) string {
	// Extract path from urlsrc (replace internal Collabora host with baseURL)
	editorPath := urlSrc
	if parsed, err := url.Parse(urlSrc); err == nil {
		editorPath = parsed.Path
		if parsed.RawQuery != "" {
			editorPath += "?" + parsed.RawQuery
		}
	}

	// Strip WOPI template placeholders: <name=VALUE&> or <name&>
	editorPath = stripWOPIPlaceholders(editorPath)

	// Build final URL
	sep := "?"
	if strings.Contains(editorPath, "?") {
		sep = "&"
	}

	return fmt.Sprintf("%s%s%sWOPISrc=%s&access_token=%s&access_token_ttl=%d",
		baseURL, editorPath, sep,
		url.QueryEscape(wopiSrc),
		url.QueryEscape(accessToken),
		ttlMs,
	)
}

// stripWOPIPlaceholders removes WOPI urlsrc template placeholders.
// Placeholders look like <ui=UI_LLCC&> or <rs=DC_LLCC&>.
// Per WOPI spec: remove the entire placeholder including angle brackets.
func stripWOPIPlaceholders(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			// Find closing >
			j := strings.IndexByte(s[i:], '>')
			if j >= 0 {
				i += j + 1
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
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
