package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// --- minimal auth/cache/renderer fakes for a full handler-level round trip ---

type previewHTTPAuth struct{ allowed bool }

func (a *previewHTTPAuth) CheckPrivilege(_ context.Context, _, _, _ string) (*port.AuthResult, error) {
	return &port.AuthResult{Allowed: a.allowed}, nil
}

type previewHTTPCache struct {
	rows map[string]model.PreviewCacheEntry
}

func newPreviewHTTPCache() *previewHTTPCache {
	return &previewHTTPCache{rows: make(map[string]model.PreviewCacheEntry)}
}

func (c *previewHTTPCache) FindBySourceID(_ context.Context, id string) (*model.PreviewCacheEntry, error) {
	if row, ok := c.rows[id]; ok {
		cp := row
		return &cp, nil
	}
	return nil, nil
}

func (c *previewHTTPCache) Upsert(_ context.Context, e model.PreviewCacheEntry) error {
	c.rows[e.SourceFileID] = e
	return nil
}

type previewHTTPRenderer struct{}

func (previewHTTPRenderer) Render(_ context.Context, _ string, content io.Reader) (io.ReadCloser, error) {
	_, _ = io.Copy(io.Discard, content)
	return io.NopCloser(bytes.NewReader([]byte("PNGBYTES"))), nil
}

func newPreviewTestRouter(h *PreviewHandler) chi.Router {
	r := chi.NewRouter()
	r.Get("/wopi-private/files/{fileID}/preview", h.ServeHTTP)
	return r
}

// TestPreviewHandler_MissingActorIsUnauthorized proves the handler itself
// also rejects a missing actor (defense in depth alongside
// ActorHeaderMiddleware, which is not mounted in this direct-handler test).
func TestPreviewHandler_MissingActorIsUnauthorized(t *testing.T) {
	h := NewPreviewHandler(nil, zap.NewNop())
	r := newPreviewTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/wopi-private/files/f1/preview", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req) // no actor stamped on context

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// TestPreviewHandler_ErrorMapping proves each PreviewService error maps to
// the status the contract requires (preview-http.md's error table).
func TestPreviewHandler_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"source not found", service.ErrDocumentNotFound, http.StatusNotFound},
		{"not authorized", service.ErrNotAuthorized, http.StatusForbidden},
		{"unsupported source type", model.ErrUnsupportedPreviewSource, http.StatusUnprocessableEntity},
		{"admission full", service.ErrRenderAdmissionFull, http.StatusServiceUnavailable},
		{"render failed", service.ErrRenderFailed, http.StatusBadGateway},
		{"unmapped error", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewPreviewHandler(nil, zap.NewNop())
			rr := httptest.NewRecorder()
			h.writeError(rr, "f1", tc.err)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

// TestPreviewHandler_AuthorizedRoundTrip exercises the full route — under
// ActorHeaderMiddleware, exactly as mounted in router.go — through a real
// PreviewService: a cold request renders and returns 200 with the
// contract's headers, and a repeat with the returned ETag is an authorized
// 304 with no further render.
func TestPreviewHandler_AuthorizedRoundTrip(t *testing.T) {
	files := newHandlerMockFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{
		ID: "src", AuthorizationPolicyID: "pol",
		MimeType:  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Size:      1,
		UpdatedAt: now,
	}
	files.files["src"] = []byte("source-bytes")

	svc := service.NewPreviewService(files, &previewHTTPAuth{allowed: true}, newPreviewHTTPCache(), previewHTTPRenderer{}, 8, zap.NewNop())
	h := NewPreviewHandler(svc, zap.NewNop())

	r := chi.NewRouter()
	r.With(ActorHeaderMiddleware).Get("/wopi-private/files/{fileID}/preview", h.ServeHTTP)

	req := httptest.NewRequest(http.MethodGet, "/wopi-private/files/src/preview", nil)
	req.Header.Set(HeaderActorID, "actor-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "private, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q", got)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag header")
	}
	if rr.Body.String() != "PNGBYTES" {
		t.Errorf("body = %q", rr.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/wopi-private/files/src/preview", nil)
	req2.Header.Set(HeaderActorID, "actor-1")
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304 for a matching authorized ETag", rr2.Code)
	}
}

// TestPreviewHandler_ForgedActorHeaderIsIgnored proves the handler trusts
// only the actor ActorHeaderMiddleware resolves onto the request context —
// a caller-supplied header alone (bypassing the middleware, as a probe of
// the gateway's own strip-then-resolve chain would target) is rejected.
func TestPreviewHandler_ForgedActorHeaderIsIgnored(t *testing.T) {
	h := NewPreviewHandler(nil, zap.NewNop())
	r := chi.NewRouter() // deliberately WITHOUT ActorHeaderMiddleware
	r.Get("/wopi-private/files/{fileID}/preview", h.ServeHTTP)

	req := httptest.NewRequest(http.MethodGet, "/wopi-private/files/src/preview", nil)
	req.Header.Set(HeaderActorID, "forged-actor")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — the handler must not read the raw header itself", rr.Code)
	}
}
