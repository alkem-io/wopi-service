package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// --- In-memory file service mock ---

type mockFileService struct {
	files map[string][]byte
}

func newMockFileService() *mockFileService {
	return &mockFileService{files: make(map[string][]byte)}
}

func (m *mockFileService) ReadFile(_ context.Context, externalID string) (io.ReadCloser, error) {
	data, ok := m.files[externalID]
	if !ok {
		return nil, ErrDocumentNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockFileService) WriteFile(_ context.Context, documentID string, content io.Reader) (*port.FileWriteResult, error) {
	data, _ := io.ReadAll(content)
	extID := "hash-" + documentID
	m.files[extID] = data
	return &port.FileWriteResult{ExternalID: extID, Size: int64(len(data))}, nil
}

func (m *mockFileService) FileExists(_ context.Context, externalID string) (bool, error) {
	_, ok := m.files[externalID]
	return ok, nil
}

// --- In-memory lock repo mock ---

type mockLockRepo struct {
	locks map[string]*model.Lock
}

func newMockLockRepo() *mockLockRepo {
	return &mockLockRepo{locks: make(map[string]*model.Lock)}
}

func (m *mockLockRepo) Create(_ context.Context, lock *model.Lock) error {
	m.locks[lock.FileID] = lock
	return nil
}

func (m *mockLockRepo) FindByFileID(_ context.Context, fileID string) (*model.Lock, error) {
	return m.locks[fileID], nil
}

func (m *mockLockRepo) UpdateLockID(_ context.Context, fileID, newLockID string, lock model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.LockID = newLockID
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}

func (m *mockLockRepo) RefreshExpiry(_ context.Context, fileID string, lock *model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}

func (m *mockLockRepo) DeleteByFileID(_ context.Context, fileID string) error {
	delete(m.locks, fileID)
	return nil
}

func (m *mockLockRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

// --- Tests ---

func makeToken(fileID string, perms string) *model.AccessToken {
	return &model.AccessToken{
		ID:          uuid.New(),
		Token:       "test-token",
		FileID:      fileID,
		ActorID:     "actor-1",
		Permissions: perms,
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}
}

func TestCheckFileInfo_Success(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{
		ID:          docID,
		ExternalID:  "ext-123",
		DisplayName: "report.docx",
		Size:        2048,
	}

	svc := NewWOPIService(docRepo, newMockLockRepo(), newMockFileService(), "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	info, err := svc.CheckFileInfo(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.BaseFileName != "report.docx" {
		t.Errorf("expected report.docx, got %s", info.BaseFileName)
	}
	if info.Size != 2048 {
		t.Errorf("expected size 2048, got %d", info.Size)
	}
	if !info.UserCanWrite {
		t.Error("expected UserCanWrite=true")
	}
	if !info.SupportsLocks {
		t.Error("expected SupportsLocks=true")
	}
}

func TestCheckFileInfo_ReadOnly(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{ID: docID, DisplayName: "file.pdf"}

	svc := NewWOPIService(docRepo, newMockLockRepo(), newMockFileService(), "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read")

	info, err := svc.CheckFileInfo(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UserCanWrite {
		t.Error("expected UserCanWrite=false for read-only token")
	}
}

func TestCheckFileInfo_DocumentNotFound(t *testing.T) {
	svc := NewWOPIService(newMockDocRepo(), newMockLockRepo(), newMockFileService(), "https://wopi.example.com", zap.NewNop())
	token := makeToken("nonexistent", "read")

	_, err := svc.CheckFileInfo(context.Background(), token)
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("expected ErrDocumentNotFound, got %v", err)
	}
}

func TestGetFile_Success(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{ID: docID, ExternalID: "ext-abc"}

	fileSvc := newMockFileService()
	fileSvc.files["ext-abc"] = []byte("file content here")

	svc := NewWOPIService(docRepo, newMockLockRepo(), fileSvc, "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read")

	reader, err := svc.GetFile(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = reader.Close() }()

	data, _ := io.ReadAll(reader)
	if string(data) != "file content here" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestPutFile_Success_NoLock(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{ID: docID, ExternalID: "old-ext"}

	fileSvc := newMockFileService()
	svc := NewWOPIService(docRepo, newMockLockRepo(), fileSvc, "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	result, err := svc.PutFile(context.Background(), token, "", strings.NewReader("new content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version")
	}
}

func TestPutFile_ReadOnlyToken(t *testing.T) {
	svc := NewWOPIService(newMockDocRepo(), newMockLockRepo(), newMockFileService(), "https://wopi.example.com", zap.NewNop())
	token := makeToken("file-1", "read")

	_, err := svc.PutFile(context.Background(), token, "", strings.NewReader("content"))
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("expected ErrNotAuthorized, got %v", err)
	}
}

func TestPutFile_LockMismatch(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID:    docID,
		LockID:    "lock-A",
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	_, err := svc.PutFile(context.Background(), token, "lock-B", strings.NewReader("content"))
	if !errors.Is(err, ErrLockMismatch) {
		t.Errorf("expected ErrLockMismatch, got %v", err)
	}
}

func TestPutFile_LockMatch(t *testing.T) {
	docID := uuid.New().String()
	docRepo := newMockDocRepo()
	docRepo.docs[docID] = &model.Document{ID: docID}

	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID:    docID,
		LockID:    "lock-A",
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	fileSvc := newMockFileService()
	svc := NewWOPIService(docRepo, lockRepo, fileSvc, "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	result, err := svc.PutFile(context.Background(), token, "lock-A", strings.NewReader("content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version")
	}
}

// --- Lock operation tests (US2) ---

func TestLock_Acquire(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())

	err := svc.Lock(context.Background(), docID, "lock-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lockRepo.locks[docID] == nil {
		t.Fatal("lock not created")
	}
	if lockRepo.locks[docID].LockID != "lock-1" {
		t.Errorf("expected lock-1, got %s", lockRepo.locks[docID].LockID)
	}
}

func TestLock_SameID_RefreshesExpiry(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	oldExpiry := time.Now().Add(5 * time.Minute)
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-1", ExpiresAt: oldExpiry,
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.Lock(context.Background(), docID, "lock-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lockRepo.locks[docID].ExpiresAt.After(oldExpiry) {
		t.Error("expected expiry to be extended")
	}
}

func TestLock_Conflict(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.Lock(context.Background(), docID, "lock-B")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
	if conflictErr.ExistingLockID != "lock-A" {
		t.Errorf("expected existing lock-A, got %s", conflictErr.ExistingLockID)
	}
}

func TestUnlock_Success(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-1", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.Unlock(context.Background(), docID, "lock-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lockRepo.locks[docID] != nil {
		t.Error("lock should be deleted")
	}
}

func TestUnlock_Mismatch(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.Unlock(context.Background(), docID, "lock-B")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}

func TestUnlock_NoLock(t *testing.T) {
	svc := NewWOPIService(newMockDocRepo(), newMockLockRepo(), newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.Unlock(context.Background(), "file-1", "lock-1")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
	if conflictErr.ExistingLockID != "" {
		t.Errorf("expected empty existing lock ID for unlocked file, got %s", conflictErr.ExistingLockID)
	}
}

func TestRefreshLock_Success(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	oldExpiry := time.Now().Add(5 * time.Minute)
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-1", ExpiresAt: oldExpiry,
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.RefreshLock(context.Background(), docID, "lock-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lockRepo.locks[docID].ExpiresAt.After(oldExpiry) {
		t.Error("expected expiry to be extended")
	}
}

func TestRefreshLock_Mismatch(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.RefreshLock(context.Background(), docID, "lock-B")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}

func TestUnlockAndRelock_Success(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "old-lock", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.UnlockAndRelock(context.Background(), docID, "new-lock", "old-lock")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lockRepo.locks[docID].LockID != "new-lock" {
		t.Errorf("expected new-lock, got %s", lockRepo.locks[docID].LockID)
	}
}

func TestUnlockAndRelock_OldLockMismatch(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(newMockDocRepo(), lockRepo, newMockFileService(), "https://wopi.example.com", zap.NewNop())
	err := svc.UnlockAndRelock(context.Background(), docID, "new-lock", "wrong-old")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}
