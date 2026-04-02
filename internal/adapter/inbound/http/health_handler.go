package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// HealthHandler handles the /health endpoint.
type HealthHandler struct {
	wopiPool *pgxpool.Pool
	natsConn *nats.Conn
	logger   *zap.Logger
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(wopiPool *pgxpool.Pool, natsConn *nats.Conn, logger *zap.Logger) *HealthHandler {
	return &HealthHandler{
		wopiPool: wopiPool,
		natsConn: natsConn,
		logger:   logger,
	}
}

type healthResponse struct {
	Status string `json:"status"`
}

// ServeHTTP handles GET /health.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.wopiPool.Ping(ctx); err != nil {
		h.logger.Warn("wopi db health check failed", zap.Error(err))
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "db_unavailable"})
		return
	}

	if h.natsConn != nil && !h.natsConn.IsConnected() {
		h.logger.Warn("nats health check failed")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "nats_unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}

// LiveHandler handles the /live endpoint (process-local, no dependency checks).
func LiveHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
