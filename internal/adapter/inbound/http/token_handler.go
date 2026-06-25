package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/service"
	"github.com/alkem-io/wopi-service/internal/obs"
)

// TokenHandler handles WOPI token issuance requests.
type TokenHandler struct {
	tokenSvc *service.TokenService
	logger   *zap.Logger
}

// NewTokenHandler creates a new TokenHandler.
func NewTokenHandler(tokenSvc *service.TokenService, logger *zap.Logger) *TokenHandler {
	return &TokenHandler{tokenSvc: tokenSvc, logger: logger}
}

type tokenRequest struct {
	DocumentID string `json:"documentId"`
}

// ServeHTTP handles POST /wopi/token.
func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	actorID := ActorIDFromContext(r.Context())
	if actorID == "" {
		http.Error(w, `{"error":"missing actor identity"}`, http.StatusUnauthorized)
		return
	}

	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.DocumentID == "" {
		http.Error(w, `{"error":"documentId is required"}`, http.StatusBadRequest)
		return
	}

	actorName := ActorNameFromContext(r.Context())
	result, err := h.tokenSvc.IssueToken(r.Context(), actorID, actorName, req.DocumentID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrDocumentNotFound):
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
		case errors.Is(err, service.ErrNotAuthorized):
			http.Error(w, `{"error":"not authorized"}`, http.StatusForbidden)
		case errors.Is(err, model.ErrUnsupportedMIME):
			http.Error(w, `{"error":"document type not supported for editing"}`, http.StatusUnprocessableEntity)
		case errors.Is(err, service.ErrUnsupportedExtension):
			http.Error(w, `{"error":"document type not supported for editing"}`, http.StatusUnprocessableEntity)
		case errors.Is(err, service.ErrNoDiscoveryData):
			h.logFailure(req.DocumentID, actorID, err)
			http.Error(w, `{"error":"editor discovery unavailable"}`, http.StatusServiceUnavailable)
		default:
			h.logFailure(req.DocumentID, actorID, err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	h.logger.Info("token issued",
		zap.String("documentId", req.DocumentID),
		zap.String("actorId", actorID),
	)

	TokenIssuanceResponse{
		AccessToken: result.AccessToken,
		TTL:         result.TTL,
		WOPISrc:     result.WOPISrc,
		EditorURL:   result.EditorURL,
	}.Render(w)
}

// logFailure emits one structured token_issuance health-signal record for a
// genuine issuance failure. Classification is independent of the HTTP status
// mapping (FR-013): both the 503 ErrNoDiscoveryData branch and the 500 default
// branch call this, and the outcome distinguishes them for alerting.
func (h *TokenHandler) logFailure(documentID, actorID string, err error) {
	h.logger.Error("token issuance failed",
		zap.String(obs.FieldEvent, obs.EventTokenIssuance),
		zap.String(obs.FieldOutcome, tokenIssuanceOutcome(err)),
		zap.String(obs.FieldDocumentID, documentID),
		zap.String(obs.FieldActorID, actorID),
		zap.Error(err),
	)
}
