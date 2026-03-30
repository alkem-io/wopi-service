package http

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// NewRouter creates and configures the chi router with all routes.
func NewRouter(
	tokenSvc *service.TokenService,
	tokenHandler *TokenHandler,
	wopiHandler *WOPIHandler,
	healthHandler *HealthHandler,
	discoveryHandler *DiscoveryHandler,
) chi.Router {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Health check — no auth
	r.Handle("/health", healthHandler)

	// Discovery — no auth (public info)
	r.Handle("/wopi/discovery", discoveryHandler)

	// Token issuance — behind Oathkeeper (JWT auth)
	r.Route("/wopi/token", func(sub chi.Router) {
		sub.Use(JWTMiddleware)
		sub.Post("/", tokenHandler.ServeHTTP)
	})

	// WOPI protocol endpoints — access token auth + proof validation
	r.Group(func(sub chi.Router) {
		sub.Use(TokenAuthMiddleware(tokenSvc))
		sub.Use(ProofMiddleware)
		RegisterWOPIRoutes(sub, wopiHandler)
	})

	return r
}
