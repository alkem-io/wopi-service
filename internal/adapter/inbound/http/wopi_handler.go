package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
	"github.com/alkem-io/wopi-service/internal/obs"
)

// renameFileEvent is the body published to the server (RenameTopic) to persist a
// rename. `documentId` is the storage Document id (= token.FileID); `displayName`
// is the new base name WITHOUT extension.
type renameFileEvent struct {
	DocumentID  string `json:"documentId"`
	DisplayName string `json:"displayName"`
}

// WOPIHandler handles WOPI protocol endpoints.
type WOPIHandler struct {
	wopiSvc   *service.WOPIService
	window    *service.ContributionWindow
	publisher port.QueuePublisher
	logger    *zap.Logger
}

// NewWOPIHandler creates a new WOPIHandler. window may be nil (contribution
// tracking is best-effort and optional); callers that want tracking pass a
// live *service.ContributionWindow. publisher may be nil (rename events are
// then dropped); pass a live publisher to persist in-editor renames.
func NewWOPIHandler(wopiSvc *service.WOPIService, window *service.ContributionWindow, publisher port.QueuePublisher, logger *zap.Logger) *WOPIHandler {
	return &WOPIHandler{wopiSvc: wopiSvc, window: window, publisher: publisher, logger: logger}
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
	case "RENAME_FILE":
		h.renameFile(w, r)
	default:
		http.Error(w, `{"error":"unknown X-WOPI-Override"}`, http.StatusBadRequest)
	}
}

// renameFile handles POST /wopi/files/{fileID} with X-WOPI-Override: RENAME_FILE.
//
// Collabora issues this whenever a document is renamed inside the editor — either
// by the user via its own Rename UI, or in response to a host Action_RenameFile
// postMessage. CheckFileInfo advertises SupportsRename + UserCanRename (writers
// only), which is what makes Collabora accept both.
//
// The server is the rename authority. We publish an OFFICE_DOCUMENT_RENAME event
// (fire-and-forget) so the server renames the CollaboraDocument the same way the
// in-app header does — updating BOTH the profile (callout title) and the backing
// file-service document — then echo the requested base name back to Collabora so
// it relabels its title bar immediately. The two stores reconcile within the
// event's processing window; on the rare failure the name simply reverts on the
// next open. X-WOPI-RequestedName is the base name without extension (Collabora
// keeps the extension); we strip defensively.
func (h *WOPIHandler) renameFile(w http.ResponseWriter, r *http.Request) {
	token := TokenFromContext(r.Context())
	if token == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if !token.HasPermission("write") {
		http.Error(w, `{"error":"not authorized"}`, http.StatusForbidden)
		return
	}

	requested := strings.TrimSpace(r.Header.Get("X-WOPI-RequestedName"))
	name := strings.TrimSuffix(requested, filepath.Ext(requested))
	if name == "" {
		// No usable requested name — fall back to the current name so Collabora
		// still gets a valid, unchanged response rather than an error.
		info, err := h.wopiSvc.CheckFileInfo(r.Context(), token)
		if err != nil {
			if errors.Is(err, service.ErrDocumentNotFound) {
				http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
				return
			}
			h.logger.Error("RenameFile failed", zap.String(obs.FieldDocumentID, token.FileID), zap.Error(err))
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		name = strings.TrimSuffix(info.BaseFileName, filepath.Ext(info.BaseFileName))
	} else if h.publisher != nil {
		// Persist authoritatively via the server. Best-effort: a publish failure
		// must not fail the in-editor rename — log and let it reconcile on reopen.
		if err := h.publisher.Publish(service.RenameTopic, renameFileEvent{
			DocumentID:  token.FileID,
			DisplayName: name,
		}); err != nil {
			h.logger.Error("RenameFile publish failed",
				zap.String(obs.FieldDocumentID, token.FileID), zap.Error(err))
		}
	}

	// WOPI RenameFile responds with the base name, extension stripped.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"Name": name})
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
		var conflictErr *service.LockConflictError
		switch {
		case errors.Is(err, service.ErrNotAuthorized):
			http.Error(w, `{"error":"not authorized"}`, http.StatusForbidden)
		case errors.As(err, &conflictErr):
			w.Header().Set("X-WOPI-Lock", conflictErr.ExistingLockID)
			http.Error(w, `{"error":"lock conflict"}`, http.StatusConflict)
		case errors.Is(err, service.ErrDocumentNotFound):
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
		default:
			h.logger.Error("save failed",
				zap.String(obs.FieldEvent, obs.EventPutFile),
				zap.String(obs.FieldOutcome, putFileOutcome(err)),
				zap.String(obs.FieldDocumentID, token.FileID),
				zap.Error(err),
			)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		}
		return
	}

	// Eligibility gate (FR-001): only a successful save of a genuine user
	// modification (not autosave/no-op, not a failed write) marks the document's
	// window. In-memory flag toggle — no added save latency. Collabora sends
	// "true" only for human edits.
	if h.window != nil && isModifiedByUser(r) {
		h.window.MarkModified(token.FileID)
	}

	lastModified := result.LastModifiedTime.UTC().Format(time.RFC3339Nano)
	w.Header().Set("X-WOPI-ItemVersion", result.Version)
	w.Header().Set("X-COOL-WOPI-Timestamp", lastModified)
	PutFileResponse{
		LastModifiedTime: lastModified,
		Version:          result.Version,
	}.Render(w)
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

// LockStatus handles GET /wopi/files/{fileID}/lock-status. It reports whether the
// document currently has an active (non-expired) WOPI lock, so callers outside the
// Collabora flow — notably alkemio-server's replace-file guard — can refuse a
// backing-file swap while the document is being edited. Read-only; the route is
// gated by the server-trusted actor header (not a WOPI access token, since this is
// not a Collabora callback).
func (h *WOPIHandler) LockStatus(w http.ResponseWriter, r *http.Request) {
	// Defense-in-depth: ActorHeaderMiddleware already rejects a missing actor,
	// but re-checking inline mirrors the token handler and lets the OpenAPI
	// generator (which reads response codes from the handler body) document 401.
	if ActorIDFromContext(r.Context()) == "" {
		http.Error(w, `{"error":"missing actor identity"}`, http.StatusUnauthorized)
		return
	}

	fileID := chi.URLParam(r, "fileID")
	if fileID == "" {
		http.Error(w, `{"error":"missing fileID"}`, http.StatusBadRequest)
		return
	}

	locked, lock, err := h.wopiSvc.HasActiveLock(r.Context(), fileID)
	if err != nil {
		h.logger.Error("lock status check failed", zap.String("fileID", fileID), zap.Error(err))
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	resp := LockStatusResponse{Locked: locked}
	if locked && lock != nil {
		resp.ExpiresAt = lock.ExpiresAt.UTC().Format(time.RFC3339)
	}
	resp.Render(w)
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

// isModifiedByUser reports whether Collabora flagged this PutFile as a genuine
// user modification via X-COOL-WOPI-IsModifiedByUser. Autosaves / no-op saves
// omit it or send "false".
func isModifiedByUser(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-COOL-WOPI-IsModifiedByUser"), "true")
}

// RegisterWOPIRoutes registers WOPI protocol routes on a chi router group.
func RegisterWOPIRoutes(r chi.Router, handler *WOPIHandler) {
	r.Get("/wopi/files/{fileID}", handler.CheckFileInfo)
	r.Get("/wopi/files/{fileID}/contents", handler.GetFile)
	r.Post("/wopi/files/{fileID}/contents", handler.PutFileContents)
	r.Post("/wopi/files/{fileID}", handler.FileOperation)
}
