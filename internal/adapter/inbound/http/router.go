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
	HealthHandler    *HealthHandler
	DiscoveryHandler *DiscoveryHandler
	ProofValidation  bool
	Logger           *zap.Logger
}

// NewRouter creates and configures the chi router with all routes.
func NewRouter(deps RouterDeps) chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Health check — no auth
	r.Handle("/health", deps.HealthHandler)

	// Discovery — no auth (public info)
	r.Handle("/wopi/discovery", deps.DiscoveryHandler)

	// Token issuance — behind Oathkeeper (JWT auth)
	r.Route("/wopi/token", func(sub chi.Router) {
		sub.Use(JWTMiddleware)
		sub.Post("/", deps.TokenHandler.ServeHTTP)
	})

	// WOPI protocol endpoints — access token auth + proof validation
	r.Group(func(sub chi.Router) {
		sub.Use(TokenAuthMiddleware(deps.TokenSvc))
		sub.Use(ProofMiddleware(deps.ProofValidation, deps.DiscoverySvc, deps.Logger))
		RegisterWOPIRoutes(sub, deps.WOPIHandler)
	})

	return r
}
