package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// stubPublisher records publishes and can be made to fail, for handler-level
// best-effort tests.
type stubPublisher struct {
	calls   int
	failErr error
}

func (s *stubPublisher) Publish(_ string, _ any) error {
	s.calls++
	return s.failErr
}
func (s *stubPublisher) Close() error { return nil }

func setupWOPIHandlerWithWindow(pub *stubPublisher) (*WOPIHandler, *handlerMockFileService, *service.ContributionWindow) {
	fileSvc := newHandlerMockFileService()
	lockRepo := newHandlerMockLockRepo()
	wopiSvc := service.NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", "https://wopi.example.com", 4*time.Hour, zap.NewNop())
	window := service.NewContributionWindow(pub, time.Hour, zap.NewNop())
	handler := NewWOPIHandler(wopiSvc, window, zap.NewNop())
	return handler, fileSvc, window
}

func putReq(docID string, body string, token *model.AccessToken, modified bool) *http.Request {
	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID+"/contents", strings.NewReader(body), token)
	req.Header.Set("X-WOPI-Override", "PUT")
	if modified {
		req.Header.Set("X-COOL-WOPI-IsModifiedByUser", "true")
	}
	return req
}

// T012: PutFile marks the window modified ONLY when X-COOL-WOPI-IsModifiedByUser
// is true; an autosave/no-op save (header absent/false) does not, so the window
// publishes nothing.
func TestPutFile_MarksModified_OnlyWhenHeaderTrue(t *testing.T) {
	t.Run("modified header true -> event", func(t *testing.T) {
		pub := &stubPublisher{}
		handler, fileSvc, window := setupWOPIHandlerWithWindow(pub)
		docID := uuid.New().String()
		fileSvc.docs[docID] = &model.Document{ID: docID}
		token := &model.AccessToken{FileID: docID, ActorID: "A", Permissions: "read,write", ExpiresAt: time.Now().Add(time.Hour)}
		// Simulate the contribution middleware feeding the actor.
		window.AddActor(docID, token.ActorID, token.HasPermission("write"))

		rr := httptest.NewRecorder()
		handler.PutFileContents(rr, putReq(docID, "edited", token, true))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}

		window.Flush()
		if pub.calls != 1 {
			t.Fatalf("expected 1 published event, got %d", pub.calls)
		}
	})

	t.Run("autosave (no header) -> no event", func(t *testing.T) {
		pub := &stubPublisher{}
		handler, fileSvc, window := setupWOPIHandlerWithWindow(pub)
		docID := uuid.New().String()
		fileSvc.docs[docID] = &model.Document{ID: docID}
		token := &model.AccessToken{FileID: docID, ActorID: "A", Permissions: "read,write", ExpiresAt: time.Now().Add(time.Hour)}
		window.AddActor(docID, token.ActorID, token.HasPermission("write"))

		rr := httptest.NewRecorder()
		handler.PutFileContents(rr, putReq(docID, "autosave", token, false))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}

		window.Flush()
		if pub.calls != 0 {
			t.Fatalf("expected no published event for autosave, got %d", pub.calls)
		}
	})
}

// T013: best-effort — a broker/publish error does not fail PutFile, and the
// flush swallows it. The PutFile response is unaffected because emission runs on
// the flush path, not the save path.
func TestPutFile_BestEffort_PublishErrorDoesNotFailSave(t *testing.T) {
	pub := &stubPublisher{failErr: errors.New("broker down")}
	handler, fileSvc, window := setupWOPIHandlerWithWindow(pub)
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}
	token := &model.AccessToken{FileID: docID, ActorID: "A", Permissions: "read,write", ExpiresAt: time.Now().Add(time.Hour)}
	window.AddActor(docID, token.ActorID, token.HasPermission("write"))

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, putReq(docID, "edited", token, true))

	// Save succeeds regardless of broker health.
	if rr.Code != http.StatusOK {
		t.Fatalf("PutFile status = %d, want 200 (publish must not affect save)", rr.Code)
	}

	// Flush attempts to publish and swallows the error.
	window.Flush()
	if pub.calls != 1 {
		t.Fatalf("expected 1 publish attempt, got %d", pub.calls)
	}
	if window.PublishFailureCount() != 1 {
		t.Errorf("expected 1 recorded publish failure, got %d", window.PublishFailureCount())
	}
	if window.EmittedCount() != 0 {
		t.Errorf("expected 0 emitted, got %d", window.EmittedCount())
	}
}
