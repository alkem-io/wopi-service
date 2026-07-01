package http

import (
	"encoding/json"
	"net/http"
)

// PutFileResponse is the WOPI PutFile success body.
//
// Collabora requires JSON with `LastModifiedTime` after every successful
// PutFile — it uses the value to confirm the host wrote the bytes and to
// reconcile the editor's in-memory state against the persisted file. When
// the body is missing or lacks LastModifiedTime, Collabora logs "Invalid
// or missing JSON in WOPI::PutFile HTTP_OK response" and the kit (editor)
// process kills its WebSocket with EPIPE. The DocBroker then enters
// "unloading" state and rejects new session attempts on the same WOPISrc
// URL until it finishes unloading — surfacing in the browser as "Failed
// to establish socket connection". Always emit this body on a successful
// save, even when no upstream timestamp is available, because Collabora
// treats the missing field as a hard error.
//
// Version is included in the body in addition to the X-WOPI-ItemVersion
// header for clients that consume it from the body.
type PutFileResponse struct {
	LastModifiedTime string `json:"LastModifiedTime"`
	Version          string `json:"Version,omitempty"`
}

// Render writes the PutFile response as JSON with 200 OK.
func (r PutFileResponse) Render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(r)
}

// TokenIssuanceResponse is returned by POST /wopi/token.
type TokenIssuanceResponse struct {
	AccessToken string `json:"accessToken"`
	TTL         int64  `json:"accessTokenTTL"`
	WOPISrc     string `json:"wopiSrc"`
	EditorURL   string `json:"editorUrl"`
}

// Render writes the response as JSON with 200 OK.
func (r TokenIssuanceResponse) Render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(r) //nolint:gosec // G117: AccessToken is intentionally returned to client
}

// LockStatusResponse is returned by GET /wopi/files/{fileID}/lock-status.
// `Locked` reports whether an active (non-expired) WOPI lock exists — i.e. the
// document is currently being edited in Collabora. `ExpiresAt` (RFC3339) is
// advisory and present only when locked. Consumed by alkemio-server's
// replace-file guard.
type LockStatusResponse struct {
	Locked    bool   `json:"locked"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// Render writes the lock-status response as JSON with 200 OK.
func (r LockStatusResponse) Render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(r)
}

// ErrorResponse is the standard error response body.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Render writes the error response as JSON.
func (r ErrorResponse) Render(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status string `json:"status"`
}

// Render writes the health response as JSON.
func (r HealthResponse) Render(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(r)
}
