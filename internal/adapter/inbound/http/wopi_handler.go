package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// WOPIHandler handles WOPI protocol endpoints.
type WOPIHandler struct {
	wopiSvc *service.WOPIService
	logger  *zap.Logger
}

// NewWOPIHandler creates a new WOPIHandler.
func NewWOPIHandler(wopiSvc *service.WOPIService, logger *zap.Logger) *WOPIHandler {
	return &WOPIHandler{wopiSvc: wopiSvc, logger: logger}
}

// CheckFileInfo handles GET /wopi/files/{fileID}.
func (h *WOPIHandler) CheckFileInfo(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	info, err := h.wopiSvc.CheckFileInfo(r.Context(), token)
	if err != nil {
		if errors.Is(err, service.ErrDocumentNotFound) {
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
			return
		}
		h.logger.Error("CheckFileInfo failed", zap.Error(err))
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// GetFile handles GET /wopi/files/{fileID}/contents.
func (h *WOPIHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	content, err := h.wopiSvc.GetFile(r.Context(), token)
	if err != nil {
		if errors.Is(err, service.ErrDocumentNotFound) {
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
			return
		}
		h.logger.Error("GetFile failed", zap.Error(err))
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer func() { _ = content.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, content); err != nil {
		h.logger.Error("GetFile stream error", zap.Error(err))
	}
}

// FileOperation handles POST /wopi/files/{fileID} — dispatches on X-WOPI-Override.
func (h *WOPIHandler) FileOperation(w http.ResponseWriter, r *http.Request) {
	override := r.Header.Get("X-WOPI-Override")

	switch override {
	case "PUT":
		h.putFile(w, r)
	case "LOCK":
		if r.Header.Get("X-WOPI-OldLock") != "" {
			h.unlockAndRelock(w, r)
		} else {
			h.lock(w, r)
		}
	case "UNLOCK":
		h.unlock(w, r)
	case "REFRESH_LOCK":
		h.refreshLock(w, r)
	default:
		http.Error(w, `{"error":"unknown X-WOPI-Override"}`, http.StatusBadRequest)
	}
}

// PutFileContents handles POST /wopi/files/{fileID}/contents.
func (h *WOPIHandler) PutFileContents(w http.ResponseWriter, r *http.Request) {
	h.putFile(w, r)
}

func (h *WOPIHandler) putFile(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	lockID := r.Header.Get("X-WOPI-Lock")

	result, err := h.wopiSvc.PutFile(r.Context(), token, lockID, r.Body)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotAuthorized):
			http.Error(w, `{"error":"not authorized"}`, http.StatusForbidden)
		case errors.Is(err, service.ErrLockMismatch):
			// Return current lock in header
			w.Header().Set("X-WOPI-Lock", lockID)
			http.Error(w, `{"error":"lock mismatch"}`, http.StatusConflict)
		case errors.Is(err, service.ErrDocumentNotFound):
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
		default:
			h.logger.Error("PutFile failed", zap.Error(err))
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("X-WOPI-ItemVersion", result.Version)
	w.Header().Set("X-COOL-WOPI-Timestamp", time.Now().UTC().Format(time.RFC3339))
	w.WriteHeader(http.StatusOK)
}

func (h *WOPIHandler) lock(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	lockID := r.Header.Get("X-WOPI-Lock")
	err := h.wopiSvc.Lock(r.Context(), token.FileID, lockID)
	if err != nil {
		h.handleLockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WOPIHandler) unlock(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	lockID := r.Header.Get("X-WOPI-Lock")
	err := h.wopiSvc.Unlock(r.Context(), token.FileID, lockID)
	if err != nil {
		h.handleLockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WOPIHandler) refreshLock(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	lockID := r.Header.Get("X-WOPI-Lock")
	err := h.wopiSvc.RefreshLock(r.Context(), token.FileID, lockID)
	if err != nil {
		h.handleLockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WOPIHandler) unlockAndRelock(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	newLockID := r.Header.Get("X-WOPI-Lock")
	oldLockID := r.Header.Get("X-WOPI-OldLock")
	err := h.wopiSvc.UnlockAndRelock(r.Context(), token.FileID, newLockID, oldLockID)
	if err != nil {
		h.handleLockError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WOPIHandler) handleLockError(w http.ResponseWriter, err error) {
	var conflictErr *service.LockConflictError
	if errors.As(err, &conflictErr) {
		w.Header().Set("X-WOPI-Lock", conflictErr.ExistingLockID)
		http.Error(w, `{"error":"lock conflict"}`, http.StatusConflict)
		return
	}
	h.logger.Error("lock operation failed", zap.Error(err))
	http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
}

// RegisterWOPIRoutes registers WOPI protocol routes on a chi router group.
func RegisterWOPIRoutes(r chi.Router, handler *WOPIHandler) {
	r.Get("/wopi/files/{fileID}", handler.CheckFileInfo)
	r.Get("/wopi/files/{fileID}/contents", handler.GetFile)
	r.Post("/wopi/files/{fileID}/contents", handler.PutFileContents)
	r.Post("/wopi/files/{fileID}", handler.FileOperation)
}
