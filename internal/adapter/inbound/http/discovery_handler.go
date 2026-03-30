package http

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// DiscoveryHandler handles the /wopi/discovery endpoint.
type DiscoveryHandler struct {
	discoverySvc *service.DiscoveryService
	logger       *zap.Logger
}

// NewDiscoveryHandler creates a new DiscoveryHandler.
func NewDiscoveryHandler(discoverySvc *service.DiscoveryService, logger *zap.Logger) *DiscoveryHandler {
	return &DiscoveryHandler{discoverySvc: discoverySvc, logger: logger}
}

// ServeHTTP handles GET /wopi/discovery.
func (h *DiscoveryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, err := h.discoverySvc.GetDiscovery(r.Context())
	if err != nil {
		h.logger.Error("discovery fetch failed", zap.Error(err))
		http.Error(w, `{"error":"discovery unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}
