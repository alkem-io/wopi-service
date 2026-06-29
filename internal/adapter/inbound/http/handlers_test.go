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
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, err
	}
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
func (m *handlerMockLockRepo) UpdateLockID(_ context.Context, fileID, currentLockID, newLockID string, lock model.Lock) error {
	existing, ok := m.locks[fileID]
	if !ok || existing.LockID != currentLockID {
		return port.ErrStaleLock
	}
	existing.LockID = newLockID
	existing.ExpiresAt = lock.ExpiresAt
	return nil
}
func (m *handlerMockLockRepo) RefreshExpiry(_ context.Context, fileID, lockID string, lock *model.Lock) error {
	existing, ok := m.locks[fileID]
	if !ok || existing.LockID != lockID {
		return port.ErrStaleLock
	}
	existing.ExpiresAt = lock.ExpiresAt
	return nil
}
func (m *handlerMockLockRepo) DeleteByFileID(_ context.Context, fileID, lockID string) error {
	existing, ok := m.locks[fileID]
	if !ok || existing.LockID != lockID {
		return port.ErrStaleLock
	}
	delete(m.locks, fileID)
	return nil
}
func (m *handlerMockLockRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }
func (m *handlerMockLockRepo) Takeover(_ context.Context, fileID, oldLockID, newLockID string, newCreatedAt, newExpiresAt time.Time) error {
	existing, ok := m.locks[fileID]
	if !ok || existing.LockID != oldLockID {
		return port.ErrStaleLock
	}
	existing.LockID = newLockID
	existing.CreatedAt = newCreatedAt
	existing.ExpiresAt = newExpiresAt
	return nil
}

// helper: create a request with a token already in context
func reqWithToken(method, path string, body io.Reader, token *model.AccessToken) *http.Request {
	req := httptest.NewRequest(method, path, body)
	ctx := context.WithValue(req.Context(), tokenContextKey, token)
	return req.WithContext(ctx)
}

func setupWOPIHandler() (*WOPIHandler, *handlerMockFileService, *handlerMockLockRepo) {
	fileSvc := newHandlerMockFileService()
	lockRepo := newHandlerMockLockRepo()
	wopiSvc := service.NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", "https://wopi.example.com", 4*time.Hour, zap.NewNop())
	handler := NewWOPIHandler(wopiSvc, nil, zap.NewNop())
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

// TestWOPIHandler_PutFile_JSONBody defends the Collabora-spec invariant:
// a successful PutFile must return JSON with LastModifiedTime in the body.
// When this is missing Collabora logs "Invalid or missing JSON in
// WOPI::PutFile HTTP_OK response", kills the kit session with EPIPE, and
// the DocBroker enters "unloading" — subsequent opens of the same file
// fail with "kind=docunloading" until the unload completes.
func TestWOPIHandler_PutFile_JSONBody(t *testing.T) {
	handler, fileSvc, _ := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID+"/contents", strings.NewReader("bytes"), token)
	req.Header.Set("X-WOPI-Override", "PUT")

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body PutFileResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("PutFile response body is not valid JSON: %v (body=%q)", err, rr.Body.String())
	}
	if body.LastModifiedTime == "" {
		t.Fatal("LastModifiedTime is empty — Collabora rejects this and kills the kit session")
	}
	if _, err := time.Parse(time.RFC3339Nano, body.LastModifiedTime); err != nil {
		t.Errorf("LastModifiedTime = %q is not RFC3339Nano: %v", body.LastModifiedTime, err)
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
		FileID: docID, LockID: "lock-A",
		CreatedAt: time.Now().Add(-5 * time.Minute), // within MaxLockLifetime so we get a real conflict
		ExpiresAt: time.Now().Add(30 * time.Minute),
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
		ID:                    docID,
		AuthorizationPolicyID: uuid.New().String(),
		MimeType:              "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}

	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	discSvc := testHandlerDiscoverySvc()
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, &stubAuthSvc{},
		discSvc,
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
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

	var resp TokenIssuanceResponse
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

// setupTokenHandler wires a TokenHandler over in-memory deps with one editable
// document, returning the handler and the token repo so tests can inspect the
// persisted AccessToken (whose ActorName becomes the CheckFileInfo
// UserFriendlyName).
func setupTokenHandler(t *testing.T) (*TokenHandler, *memTokenRepo, string) {
	t.Helper()
	fileSvc := newHandlerMockFileService()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{
		ID:                    docID,
		AuthorizationPolicyID: uuid.New().String(),
		MimeType:              "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}
	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, &stubAuthSvc{},
		testHandlerDiscoverySvc(),
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)
	return NewTokenHandler(tokenSvc, zap.NewNop()), tokenRepo, docID
}

// onlyIssuedToken returns the single AccessToken persisted during a successful
// issuance, failing the test if there is not exactly one.
func onlyIssuedToken(t *testing.T, repo *memTokenRepo) *model.AccessToken {
	t.Helper()
	if len(repo.tokens) != 1 {
		t.Fatalf("expected exactly 1 issued token, got %d", len(repo.tokens))
	}
	for _, tok := range repo.tokens {
		return tok
	}
	return nil
}

// #6170: alkemio-server resolves the actor's name and sends it in the token
// request body; it must land on the AccessToken so the editor shows the real
// name instead of "UnknownUser".
func TestTokenHandler_UsesActorNameFromBody(t *testing.T) {
	handler, tokenRepo, docID := setupTokenHandler(t)

	body, _ := json.Marshal(map[string]string{
		"documentId": docID,
		"actorName":  "Ada Lovelace",
	})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), actorIDKey, "actor-123"))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if got := onlyIssuedToken(t, tokenRepo).ActorName; got != "Ada Lovelace" {
		t.Errorf("ActorName = %q, want %q", got, "Ada Lovelace")
	}
}

// A non-ASCII name must survive the JSON body round-trip untouched — the reason
// the name travels in the body rather than an HTTP header.
func TestTokenHandler_PreservesUnicodeActorName(t *testing.T) {
	handler, tokenRepo, docID := setupTokenHandler(t)

	const name = "Søren 李雷 O'Brien"
	body, _ := json.Marshal(map[string]string{"documentId": docID, "actorName": name})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), actorIDKey, "actor-123"))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if got := onlyIssuedToken(t, tokenRepo).ActorName; got != name {
		t.Errorf("ActorName = %q, want %q", got, name)
	}
}

// When the body omits actorName, the handler falls back to any name carried on
// the context (the legacy path) rather than overwriting it with "".
func TestTokenHandler_FallsBackToContextActorName(t *testing.T) {
	handler, tokenRepo, docID := setupTokenHandler(t)

	body, _ := json.Marshal(map[string]string{"documentId": docID})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-123")
	ctx = context.WithValue(ctx, actorNameKey, "Context Name")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if got := onlyIssuedToken(t, tokenRepo).ActorName; got != "Context Name" {
		t.Errorf("ActorName = %q, want %q", got, "Context Name")
	}
}

// With no name in the body and none on the context, ActorName stays empty so
// the WOPI service applies its own fallback — issuance must still succeed.
func TestTokenHandler_NoActorNameLeavesEmpty(t *testing.T) {
	handler, tokenRepo, docID := setupTokenHandler(t)

	body, _ := json.Marshal(map[string]string{"documentId": docID})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), actorIDKey, "actor-123"))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if got := onlyIssuedToken(t, tokenRepo).ActorName; got != "" {
		t.Errorf("ActorName = %q, want empty", got)
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

func testHandlerDiscoverySvc() *service.DiscoveryService {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Writer", Name: "edit", Ext: "docx", URLSrc: "http://collabora:9980/browser/dist/cool.html?"},
			{App: "Writer", Name: "view", Ext: "docx", URLSrc: "http://collabora:9980/browser/dist/cool.html?"},
		},
	}
	client := &mockDiscoveryClientForHandler{data: data}
	svc := service.NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background())
	return svc
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

// --- GetFile error paths ---

func TestWOPIHandler_GetFile_FileNotFound(t *testing.T) {
	handler, fileSvc, _ := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}
	// No file in fileSvc.files → ReadFile returns ErrDocumentNotFound

	token := &model.AccessToken{FileID: docID, Permissions: "read",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	rr := httptest.NewRecorder()
	handler.GetFile(rr, reqWithToken(http.MethodGet, "/wopi/files/"+docID+"/contents", nil, token))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestWOPIHandler_GetFile_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	rr := httptest.NewRecorder()
	handler.GetFile(rr, httptest.NewRequest(http.MethodGet, "/wopi/files/f1/contents", nil))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// --- PutFile error paths ---

func TestWOPIHandler_PutFile_NotAuthorized(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	token := &model.AccessToken{FileID: "f1", Permissions: "read",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/f1/contents", strings.NewReader("data"), token)
	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestWOPIHandler_PutFile_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	req := httptest.NewRequest(http.MethodPost, "/wopi/files/f1/contents", strings.NewReader("data"))
	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWOPIHandler_PutFile_LockMismatch(t *testing.T) {
	handler, fileSvc, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID+"/contents", strings.NewReader("data"), token)
	req.Header.Set("X-WOPI-Lock", "wrong-lock")
	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rr.Code)
	}
}

// --- Lock handler no-token paths ---

func TestWOPIHandler_Lock_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	req := httptest.NewRequest(http.MethodPost, "/wopi/files/f1", nil)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWOPIHandler_Unlock_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	req := httptest.NewRequest(http.MethodPost, "/wopi/files/f1", nil)
	req.Header.Set("X-WOPI-Override", "UNLOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWOPIHandler_RefreshLock_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	req := httptest.NewRequest(http.MethodPost, "/wopi/files/f1", nil)
	req.Header.Set("X-WOPI-Override", "REFRESH_LOCK")
	req.Header.Set("X-WOPI-Lock", "lock-1")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestWOPIHandler_UnlockAndRelock_NoToken(t *testing.T) {
	handler, _, _ := setupWOPIHandler()
	req := httptest.NewRequest(http.MethodPost, "/wopi/files/f1", nil)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "new")
	req.Header.Set("X-WOPI-OldLock", "old")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// --- Unlock conflict paths ---

func TestWOPIHandler_Unlock_Conflict(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "UNLOCK")
	req.Header.Set("X-WOPI-Lock", "wrong-lock")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rr.Code)
	}
	if rr.Header().Get("X-WOPI-Lock") != "lock-A" {
		t.Errorf("X-WOPI-Lock = %q, want lock-A", rr.Header().Get("X-WOPI-Lock"))
	}
}

func TestWOPIHandler_RefreshLock_Conflict(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "REFRESH_LOCK")
	req.Header.Set("X-WOPI-Lock", "wrong-lock")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rr.Code)
	}
}

func TestWOPIHandler_UnlockAndRelock_Conflict(t *testing.T) {
	handler, _, lockRepo := setupWOPIHandler()
	docID := uuid.New().String()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	token := &model.AccessToken{FileID: docID, Permissions: "read,write",
		ExpiresAt: time.Now().Add(1 * time.Hour)}

	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID, nil, token)
	req.Header.Set("X-WOPI-Override", "LOCK")
	req.Header.Set("X-WOPI-Lock", "new-lock")
	req.Header.Set("X-WOPI-OldLock", "wrong-old")
	rr := httptest.NewRecorder()
	handler.FileOperation(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rr.Code)
	}
}

// --- Token handler error paths ---

func TestTokenHandler_DocumentNotFound(t *testing.T) {
	fileSvc := newHandlerMockFileService()
	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, &stubAuthSvc{},
		nil,
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)
	handler := NewTokenHandler(tokenSvc, zap.NewNop())

	body, _ := json.Marshal(map[string]string{"documentId": "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestTokenHandler_NotAuthorized(t *testing.T) {
	fileSvc := newHandlerMockFileService()
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{
		ID: docID, AuthorizationPolicyID: uuid.New().String(),
	}

	// Auth service that denies everything
	denyAuth := &denyAuthSvc{}
	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, denyAuth,
		nil,
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)
	handler := NewTokenHandler(tokenSvc, zap.NewNop())

	body, _ := json.Marshal(map[string]string{"documentId": docID})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

type denyAuthSvc struct{}

func (s *denyAuthSvc) CheckPrivilege(_ context.Context, _, _, _ string) (*port.AuthResult, error) {
	return &port.AuthResult{Allowed: false, Reason: "denied"}, nil
}

func TestTokenHandler_InvalidBody(t *testing.T) {
	handler := NewTokenHandler(nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/wopi/token", strings.NewReader("not json"))
	ctx := context.WithValue(req.Context(), actorIDKey, "actor-1")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// --- Discovery handler error path ---

func TestDiscoveryHandler_Unavailable(t *testing.T) {
	client := &mockDiscoveryClientForHandler{err: io.ErrUnexpectedEOF}
	discSvc := service.NewDiscoveryService(client, zap.NewNop())
	handler := NewDiscoveryHandler(discSvc, zap.NewNop())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/wopi/discovery", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// --- NewRouter test ---

func TestNewRouter_Constructs(t *testing.T) {
	fileSvc := newHandlerMockFileService()
	tokenRepo := &memTokenRepo{tokens: make(map[string]*model.AccessToken)}
	tokenSvc := service.NewTokenService(
		tokenRepo, fileSvc, &stubAuthSvc{},
		nil,
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)
	wopiSvc := service.NewWOPIService(fileSvc, newHandlerMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", 4*time.Hour, zap.NewNop())

	tokenHandler := NewTokenHandler(tokenSvc, zap.NewNop())
	wopiHandler := NewWOPIHandler(wopiSvc, nil, zap.NewNop())
	discClient := &mockDiscoveryClientForHandler{data: &port.DiscoveryData{}}
	discSvc := service.NewDiscoveryService(discClient, zap.NewNop())
	discoveryHandler := NewDiscoveryHandler(discSvc, zap.NewNop())

	// HealthHandler needs real pool/conn — just test that NewRouter doesn't panic with nil
	// We'll skip health in this test
	router := NewRouter(RouterDeps{
		TokenSvc:         tokenSvc,
		DiscoverySvc:     discSvc,
		TokenHandler:     tokenHandler,
		WOPIHandler:      wopiHandler,
		HealthHandler:    nil,
		DiscoveryHandler: discoveryHandler,
		ProofValidation:  false,
		Logger:           zap.NewNop(),
	})
	if router == nil {
		t.Error("expected non-nil router")
	}
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

// --- ProofMiddleware tests ---

func TestProofMiddleware_Disabled(t *testing.T) {
	var called bool
	mw := ProofMiddleware(false, nil, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Error("expected next handler to be called when disabled")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestProofMiddleware_Enabled_NoKeys(t *testing.T) {
	discClient := &mockDiscoveryClientForHandler{err: io.ErrUnexpectedEOF}
	discSvc := service.NewDiscoveryService(discClient, zap.NewNop())

	mw := ProofMiddleware(true, discSvc, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when no keys available", rr.Code)
	}
}

func TestProofMiddleware_Enabled_MissingHeaders(t *testing.T) {
	data := &port.DiscoveryData{
		ProofKey: port.ProofKey{Modulus: "abc", Exponent: "def"},
	}
	discClient := &mockDiscoveryClientForHandler{data: data}
	discSvc := service.NewDiscoveryService(discClient, zap.NewNop())
	// Prime the cache
	_, _ = discSvc.GetDiscovery(context.Background())

	mw := ProofMiddleware(true, discSvc, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/wopi/files/f1?access_token=tok", nil)
	// No X-WOPI-Proof headers
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for missing proof headers", rr.Code)
	}
}
