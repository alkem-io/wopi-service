package http

import (
	"encoding/json"
	"net/http"
)

// TokenIssuanceResponse is returned by POST /wopi/token.
type TokenIssuanceResponse struct {
	AccessToken string `json:"accessToken"`
	TTL         int64  `json:"accessTokenTTL"`
	WOPISrc     string `json:"wopiSrc"`
}

// Render writes the response as JSON with 200 OK.
func (r TokenIssuanceResponse) Render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(r) //nolint:gosec // G117: AccessToken is intentionally returned to client
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
