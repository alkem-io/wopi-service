package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// TestRouter_PreviewOnlyUnderProtectedRoot proves the preview handler is
// reachable at the protected /wopi-private root and NOT reachable through
// the public /wopi protocol root. A 401 (missing actor, returned by
// ActorHeaderMiddleware before the handler runs) proves the route
// matched; a bare chi 404 proves it did not.
func TestRouter_PreviewOnlyUnderProtectedRoot(t *testing.T) {
	deps := RouterDeps{
		TokenHandler:     &TokenHandler{},
		WOPIHandler:      &WOPIHandler{},
		PreviewHandler:   &PreviewHandler{},
		HealthHandler:    &HealthHandler{},
		DiscoveryHandler: &DiscoveryHandler{},
		Logger:           zap.NewNop(),
	}
	r := NewRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/wopi-private/files/f1/preview", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("/wopi-private/.../preview status = %d, want 401 (route must exist and require an actor)", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/wopi/files/f1/preview", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("/wopi/.../preview status = %d, want 404 (no preview alias under the public root)", rr2.Code)
	}
}
