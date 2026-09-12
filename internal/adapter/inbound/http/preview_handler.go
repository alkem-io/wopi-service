package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// PreviewHandler serves GET /wopi-private/files/{fileID}/preview, the one
// browser-facing, identity-gated Collabora preview endpoint.
type PreviewHandler struct {
	svc    *service.PreviewService
	logger *zap.Logger
}

// NewPreviewHandler creates a PreviewHandler.
func NewPreviewHandler(svc *service.PreviewService, logger *zap.Logger) *PreviewHandler {
	return &PreviewHandler{svc: svc, logger: logger}
}

// ServeHTTP resolves and streams the preview. Actor resolution and
// rejection of a missing actor happen in ActorHeaderMiddleware first.
func (h *PreviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actorID := ActorIDFromContext(r.Context())
	if actorID == "" {
		http.Error(w, `{"error":"missing actor identity"}`, http.StatusUnauthorized)
		return
	}
	fileID := chi.URLParam(r, "fileID")
	if fileID == "" {
		http.Error(w, `{"error":"missing fileID"}`, http.StatusBadRequest)
		return
	}

	res, err := h.svc.Resolve(r.Context(), actorID, fileID, r.Header.Get("If-None-Match"))
	if err != nil {
		h.writeError(w, fileID, err)
		return
	}

	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	w.Header().Set("ETag", res.ETag)
	if res.NotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	defer func() { _ = res.Body.Close() }()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, res.Body); err != nil {
		h.logger.Warn("preview stream error", zap.String("fileID", fileID), zap.Error(err))
	}
}

func (h *PreviewHandler) writeError(w http.ResponseWriter, fileID string, err error) {
	switch {
	case errors.Is(err, service.ErrDocumentNotFound):
		http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
	case errors.Is(err, service.ErrNotAuthorized):
		http.Error(w, `{"error":"not authorized"}`, http.StatusForbidden)
	case errors.Is(err, model.ErrUnsupportedPreviewSource):
		http.Error(w, `{"error":"document type not supported for preview"}`, http.StatusUnprocessableEntity)
	case errors.Is(err, service.ErrRenderAdmissionFull):
		http.Error(w, `{"error":"preview render capacity exceeded"}`, http.StatusServiceUnavailable)
	case errors.Is(err, service.ErrRenderFailed):
		h.logger.Error("preview render failed", zap.String("fileID", fileID), zap.Error(err))
		http.Error(w, `{"error":"preview render failed"}`, http.StatusBadGateway)
	default:
		h.logger.Error("preview resolution failed", zap.String("fileID", fileID), zap.Error(err))
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
	}
}
