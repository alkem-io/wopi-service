package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// --- Full mock FileService for handler tests ---

type handlerMockFileService struct {
	docs  map[string]*model.Document
	files map[string][]byte
}

func newHandlerMockFileService() *handlerMockFileService {
	return &handlerMockFileService{
		docs:  make(map[string]*model.Document),
		files: make(map[string][]byte),
	}
}

func (m *handlerMockFileService) FindByID(_ context.Context, id string) (*model.Document, error) {
	return m.docs[id], nil
}

func (m *handlerMockFileService) ReadFile(_ context.Context, id string) (io.ReadCloser, error) {
	data, ok := m.files[id]
	if !ok {
		return nil, service.ErrDocumentNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *handlerMockFileService) WriteFile(_ context.Context, id string, content io.Reader) (*port.FileWriteResult, error) {
	data, _ := io.ReadAll(content)
	m.files[id] = data
	return &port.FileWriteResult{ExternalID: "new-hash", Size: int64(len(data))}, nil
}

func (m *handlerMockFileService) FileExists(_ context.Context, id string) (bool, error) {
	_, ok := m.files[id]
	return ok, nil
}

// --- Mock lock repo ---

type handlerMockLockRepo struct {
	locks map[string]*model.Lock
}

func newHandlerMockLockRepo() *handlerMockLockRepo {
	return &handlerMockLockRepo{locks: make(map[string]*model.Lock)}
}

func (m *handlerMockLockRepo) Create(_ context.Context, lock *model.Lock) error {
	m.locks[lock.FileID] = lock
	return nil
}
func (m *handlerMockLockRepo) FindByFileID(_ context.Context, fileID string) (*model.Lock, error) {
	return m.locks[fileID], nil
}
func (m *handlerMockLockRepo) UpdateLockID(_ context.Context, fileID, newLockID string, lock model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.LockID = newLockID
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}
func (m *handlerMockLockRepo) RefreshExpiry(_ context.Context, fileID string, lock *model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}
func (m *handlerMockLockRepo) DeleteByFileID(_ context.Context, fileID string) error {
	delete(m.locks, fileID)
	return nil
}
func (m *handlerMockLockRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

// helper: create a request with a token already in context
func reqWithToken(method, path string, body io.Reader, token *model.AccessToken) *http.Request {
	req := httptest.NewRequest(method, path, body)
	ctx := context.WithValue(req.Context(), tokenContextKey, token)
	return req.WithContext(ctx)
}

func setupWOPIHandler() (*WOPIHandler, *handlerMockFileService, *handlerMockLockRepo) {
	fileSvc := newHandlerMockFileService()
	lockRepo := newHandlerMockLockRepo()
	wopiSvc := service.NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", zap.NewNop())
	handler := NewWOPIHandler(wopiSvc, zap.NewNop())
	return handler, fileSvc, lockRepo
}

// --- CheckFileInfo tests ---

func TestWOPIHandler_CheckFileInfo_Success(t *testing.T) {
	handler, fileSvc, _ := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{
		ID: docID, DisplayName: "test.docx", Size: 1024, ExternalID: "ext-1",
	}

	token := &model.AccessToken{FileID: docID, ActorID: "actor-1", Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	rr := httptest.NewRecorder()
	handler.CheckFileInfo(rr, reqWithToken(http.MethodGet, "/wopi/files/"+docID, nil, token))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var info model.FileInfo
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if info.BaseFileName != "test.docx" {
		t.Errorf("BaseFileName = %q, want test.docx", info.BaseFileName)
	}
	if !info.UserCanWrite {
		t.Error("expected UserCanWrite=true")
	}
}

func TestWOPIHandler_CheckFileInfo_NotFound(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	token := &model.AccessToken{FileID: "missing", Permissions: "read",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	rr := httptest.NewRecorder()
	handler.CheckFileInfo(rr, reqWithToken(http.MethodGet, "/wopi/files/missing", nil, token))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestWOPIHandler_CheckFileInfo_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	rr := httptest.NewRecorder()
	handler.CheckFileInfo(rr, httptest.NewRequest(http.MethodGet, "/wopi/files/f1", nil))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// --- GetFile tests ---

func TestWOPIHandler_GetFile_Success(t *testing.T) {
	handler, fileSvc, _ := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID, ExternalID: "ext-1"}
	fileSvc.files[docID] = []byte("file content")

	token := &model.AccessToken{FileID: docID, Permissions: "read",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	rr := httptest.NewRecorder()
	handler.GetFile(rr, reqWithToken(http.MethodGet, "/wopi/files/"+docID+"/contents", nil, token))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "file content" {
		t.Errorf("body = %q, want 'file content'", rr.Body.String())
	}
}

// --- PutFile tests ---

func TestWOPIHandler_PutFile_Success(t *testing.T) {
	handler, fileSvc, _ := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID+"/contents", strings.NewReader("new content"), token)
	req.Header.Set("X-WOPI-Override", "PUT")

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("X-COOL-WOPI-Timestamp") == "" {
		t.Error("expected X-COOL-WOPI-Timestamp header")
	}
}

// --- Lock operation tests ---

func TestWOPIHandler_Lock_Acquire(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	docID := uuid.New().String()

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestWOPIHandler_Lock_Conflict(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "lock-B")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rr.Code)
	}
	if rr.Header().Get("X-WOPI-Lock") != "lock-A" {
		t.Errorf("X-WOPI-Lock = %q, want lock-A", rr.Header().Get("X-WOPI-Lock"))
	}
}

func TestWOPIHandler_Unlock_Success(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-1", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "UNLOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestWOPIHandler_RefreshLock(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-1", ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "REFRESH_LOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestWOPIHandler_UnlockAndRelock(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "old-lock", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "new-lock")
	req.Header.Set("X-WOPI-OldLock", "old-lock")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestWOPIHandler_UnknownOverride(t *testing.T) {
	handler, _, _ := setupWOPIHandler()

	token := &model.AccessToken{FileID: "f1", Permissions: "read",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/f1", nil, token)
	req.Header.Set("X-WOPI-Override", "UNKNOWN")

	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// --- Token handler tests ---

func TestTokenHandler_Success(t *testing.T) {
	fileSvc := newHandlerMockFileService()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{
		ID: docID, AuthorizationPolicyID: uuid.New().String(),
	}

	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, &stubAuthSvc{}, &stubSessionRepo{},
		"secret", "https://wopi.example.com", zap.NewNop(),
	)
	handler := NewTokenHandler(tokenSvc, zap.NewNop())

	body, _ := json.Marshal(map[string]string{"documentId": docID})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-123")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}

	var resp tokenResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("expected non-empty access token")
	}
	if resp.WOPISrc == "" {
		t.Error("expected non-empty WOPISrc")
	}
}

func TestTokenHandler_MissingActorID(t *testing.T) {
	handler := NewTokenHandler(nil, zap.NewNop())

	body, _ := json.Marshal(map[string]string{"documentId": "doc-1"})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestTokenHandler_MissingDocumentID(t *testing.T) {
	handler := NewTokenHandler(nil, zap.NewNop())

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestTokenHandler_WrongMethod(t *testing.T) {
	handler := NewTokenHandler(nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/wopi/token", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// --- Discovery handler tests ---

func TestDiscoveryHandler_Success(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{{App: "Word", Ext: "docx"}},
	}
	discSvc := service.NewDiscoveryService(&mockDiscoveryClientForHandler{data: data}, zap.NewNop())
	handler := NewDiscoveryHandler(discSvc, zap.NewNop())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/wopi/discovery", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

type mockDiscoveryClientForHandler struct {
	data *port.DiscoveryData
	err  error
}

func (m *mockDiscoveryClientForHandler) FetchDiscovery(_ context.Context) (*port.DiscoveryData, error) {
	return m.data, m.err
}

// --- RegisterWOPIRoutes test ---

func TestRegisterWOPIRoutes(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	r := chi.NewRouter()
	RegisterWOPIRoutes(r, handler)

	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

// --- ProofMiddleware test ---

func TestProofMiddleware_PassThrough(t *testing.T) {
	var called bool
	handler := ProofMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Error("expected next handler to be called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}
