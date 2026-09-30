package http

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// RouterDeps holds dependencies for the router.
type RouterDeps struct {
	TokenSvc         *service.TokenService
	DiscoverySvc     *service.DiscoveryService
	TokenHandler     *TokenHandler
	WOPIHandler      *WOPIHandler
	PreviewHandler   *PreviewHandler
	HealthHandler    *HealthHandler
	DiscoveryHandler *DiscoveryHandler
	ContributionWnd  *service.ContributionWindow
	ProofValidation  bool
	Logger           *zap.Logger
}

// NewRouter creates and configures the chi router with all routes.
func NewRouter(deps RouterDeps) chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	// RealIP is deprecated upstream (spoofable headers); here it only feeds
	// the request logger's remoteAddr field — identity/authz never read
	// r.RemoteAddr. Kept for log continuity; revisit separately.
	r.Use(middleware.RealIP) //nolint:staticcheck // SA1019: see comment above
	r.Use(middleware.Recoverer)
	r.Use(RequestLogger(deps.Logger))

	// Health checks — no auth
	r.Handle("/health", deps.HealthHandler) // readiness: checks DB + NATS
	r.HandleFunc("/live", LiveHandler)      // liveness: process-local only

	// Discovery — no auth (public info)
	r.Handle("/wopi/discovery", deps.DiscoveryHandler)

	// Token issuance — identity supplied by Traefik's alkemio-resolve
	// forwardAuth as X-Alkemio-Actor-Id header.
	r.With(ActorHeaderMiddleware).Post("/wopi/token", deps.TokenHandler.ServeHTTP)

	// Document lock status — read-only query used by alkemio-server's replace-file
	// guard to refuse a backing-file swap while the document is being edited.
	// Identity-gated by the server-trusted actor header (NOT a WOPI access-token
	// route — this is not a Collabora callback). Unlike /wopi/token it does NOT
	// authorize the caller against the document: the only thing exposed is whether
	// the file is currently open for editing (not document-content-sensitive), and
	// the guard cares solely about that fact, not who is asking.
	r.With(ActorHeaderMiddleware).Get("/wopi/files/{fileID}/lock-status", deps.WOPIHandler.LockStatus)

	// Collabora document preview — mounted ONLY under the protected
	// /wopi-private root (never public /wopi, never the WOPI access-token
	// middleware group). The gateway strips caller-supplied actor headers
	// and rewrites the external /api/private/wopi/... contract here.
	//
	// This handler is safe only once the gateway's public /wopi route
	// matches segment-safely (so it cannot lexically capture /wopi-private)
	// and strips client-supplied actor headers before this service ever
	// sees the request. Deploying an image carrying this handler ahead of
	// that gateway change makes it reachable, unauthenticated, on the
	// public router: do not bump the wopi-service image tag in any
	// environment before confirming the live gateway route there already
	// enforces both properties.
	r.With(ActorHeaderMiddleware).Get("/wopi-private/files/{fileID}/preview", deps.PreviewHandler.ServeHTTP)

	// WOPI protocol endpoints — access token auth + proof validation
	r.Group(func(sub chi.Router) {
		sub.Use(TokenAuthMiddleware(deps.TokenSvc))
		sub.Use(ProofMiddleware(deps.ProofValidation, deps.DiscoverySvc, deps.Logger))
		// Record active actors per document for contribution windowing (FR-002/003).
		// Runs after auth so the validated token is in context.
		sub.Use(ContributionMiddleware(deps.ContributionWnd))
		RegisterWOPIRoutes(sub, deps.WOPIHandler)
	})

	return r
}
