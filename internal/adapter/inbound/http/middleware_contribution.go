package http

import (
	"net/http"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// ContributionMiddleware records the requesting actor as active on the document
// for the current contribution window, split by token permission
// (write-capable → writeActors, else readonlyActors) — FR-002/FR-003.
//
// It runs after TokenAuthMiddleware (so the validated token is in context) on
// every WOPI file route (CheckFileInfo/GetFile/PutFile/Lock/...), so all
// operations count toward presence. Window is in-memory: this adds no I/O to
// the request path. A nil window disables tracking (best-effort/optional).
func ContributionMiddleware(window *service.ContributionWindow) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if window != nil {
				if token := TokenFromContext(r.Context()); token != nil {
					window.AddActor(token.FileID, token.ActorID, token.HasPermission("write"))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
