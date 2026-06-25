package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// collaboraProbeTimeout bounds the per-request Collabora reachability probe so a
// hung or slow Collabora yields a prompt "unreachable" without delaying the
// readiness response (FR-014). It is independent of the 30s discovery-fetch
// timeout.
const collaboraProbeTimeout = 2 * time.Second

// dbPinger is the subset of *pgxpool.Pool the health check needs. An interface
// so the handler is unit-testable without a live database.
type dbPinger interface {
	Ping(ctx context.Context) error
}

// collaboraProber reports current Collabora reachability. Satisfied by the
// domain *service.DiscoveryService; defined here (consumer side) so the handler
// depends on a behaviour, not a concrete adapter.
type collaboraProber interface {
	Probe(ctx context.Context) (reachable bool, lastSuccess time.Time)
}

// HealthHandler handles the /health endpoint.
type HealthHandler struct {
	wopiPool dbPinger
	natsConn *nats.Conn
	prober   collaboraProber
	logger   *zap.Logger
}

// NewHealthHandler creates a new HealthHandler. prober may be nil (Collabora
// reachability is then omitted from the response); the hard-dependency checks
// (db, nats) are unaffected.
func NewHealthHandler(wopiPool dbPinger, natsConn *nats.Conn, prober collaboraProber, logger *zap.Logger) *HealthHandler {
	return &HealthHandler{
		wopiPool: wopiPool,
		natsConn: natsConn,
		prober:   prober,
		logger:   logger,
	}
}

type healthResponse struct {
	Status               string `json:"status"`
	Collabora            string `json:"collabora,omitempty"`              // "reachable" | "unreachable"
	CollaboraLastSuccess string `json:"collabora_last_success,omitempty"` // RFC3339; omitted if never reached
}

// Render writes the response as JSON with the given status code.
func (r healthResponse) Render(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(r)
}

// ServeHTTP handles GET /health. Hard dependencies (own db; nats when
// configured) determine the HTTP status. Collabora is a soft dependency: it is
// probed once per request (≤2s) on the healthy path and reported in the body
// only — it never changes the status code (FR-009/FR-010).
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.wopiPool.Ping(ctx); err != nil {
		h.logger.Warn("wopi db health check failed", zap.Error(err))
		healthResponse{Status: "db_unavailable"}.Render(w, http.StatusServiceUnavailable)
		return
	}

	if h.natsConn != nil && !h.natsConn.IsConnected() {
		h.logger.Warn("nats health check failed")
		healthResponse{Status: "nats_unavailable"}.Render(w, http.StatusServiceUnavailable)
		return
	}

	resp := healthResponse{Status: "ok"}
	if h.prober != nil {
		probeCtx, probeCancel := context.WithTimeout(r.Context(), collaboraProbeTimeout)
		defer probeCancel()

		reachable, lastSuccess := h.prober.Probe(probeCtx)
		if reachable {
			resp.Collabora = "reachable"
		} else {
			resp.Collabora = "unreachable"
		}
		if !lastSuccess.IsZero() {
			resp.CollaboraLastSuccess = lastSuccess.UTC().Format(time.RFC3339)
		}
	}
	resp.Render(w, http.StatusOK)
}

// LiveHandler handles the /live endpoint (process-local, no dependency checks).
func LiveHandler(w http.ResponseWriter, _ *http.Request) {
	healthResponse{Status: "ok"}.Render(w, http.StatusOK)
}
