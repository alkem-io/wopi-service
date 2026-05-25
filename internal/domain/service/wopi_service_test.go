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
	docs  map[string]*model.Document
	files map[string][]byte
}

func newMockFileService() *mockFileService {
	return &mockFileService{
		docs:  make(map[string]*model.Document),
		files: make(map[string][]byte),
	}
}

func (m *mockFileService) FindByID(_ context.Context, documentID string) (*model.Document, error) {
	return m.docs[documentID], nil
}

func (m *mockFileService) ReadFile(_ context.Context, documentID string) (io.ReadCloser, error) {
	data, ok := m.files[documentID]
	if !ok {
		return nil, ErrDocumentNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockFileService) WriteFile(_ context.Context, documentID string, content io.Reader) (*port.FileWriteResult, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, err
	}
	extID := "hash-" + documentID
	m.files[documentID] = data
	return &port.FileWriteResult{ExternalID: extID, Size: int64(len(data))}, nil
}

func (m *mockFileService) FileExists(_ context.Context, documentID string) (bool, error) {
	_, ok := m.files[documentID]
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

func (m *mockLockRepo) UpdateLockID(_ context.Context, fileID, _, newLockID string, lock model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.LockID = newLockID
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}

func (m *mockLockRepo) RefreshExpiry(_ context.Context, fileID, _ string, lock *model.Lock) error {
	if existing, ok := m.locks[fileID]; ok {
		existing.ExpiresAt = lock.ExpiresAt
	}
	return nil
}

func (m *mockLockRepo) DeleteByFileID(_ context.Context, fileID, _ string) error {
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
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{
		ID:          docID,
		ExternalID:  "ext-123",
		DisplayName: "report.docx",
		Size:        2048,
	}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID, DisplayName: "file.pdf"}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read")

	info, err := svc.CheckFileInfo(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UserCanWrite {
		t.Error("expected UserCanWrite=false for read-only token")
	}
}

// TestCheckFileInfo_OwnerID_FromCreatedBy defends the WOPI spec invariant
// that OwnerId is stable per-file (file owner identity, not caller identity).
// Before the fix this returned token.ActorID, which broke Collabora's
// DocBroker state whenever a second user opened the same document.
func TestCheckFileInfo_OwnerID_FromCreatedBy(t *testing.T) {
	docID := uuid.New().String()
	creatorID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{
		ID:          docID,
		DisplayName: "file.docx",
		CreatedBy:   creatorID,
	}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	info, err := svc.CheckFileInfo(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OwnerID != creatorID {
		t.Errorf("OwnerID = %q, want %q (CreatedBy)", info.OwnerID, creatorID)
	}
	if info.OwnerID == token.ActorID {
		t.Error("OwnerID must not equal token.ActorID — that is the previous bug")
	}
	if info.UserID != token.ActorID {
		t.Errorf("UserID = %q, want token.ActorID %q", info.UserID, token.ActorID)
	}
}

// TestCheckFileInfo_OwnerID_FallbackToDocumentID covers documents with no
// recorded creator (legacy data, system-created). OwnerId must still be a
// stable per-file value — using the document ID guarantees that.
func TestCheckFileInfo_OwnerID_FallbackToDocumentID(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{
		ID:          docID,
		DisplayName: "file.docx",
		// CreatedBy intentionally empty
	}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read")

	info, err := svc.CheckFileInfo(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OwnerID != docID {
		t.Errorf("OwnerID = %q, want %q (fallback to document ID)", info.OwnerID, docID)
	}
}

// TestCheckFileInfo_LastModifiedTime_FromUpdatedAt confirms we emit
// LastModifiedTime as ISO 8601 UTC when file-service-go reports one,
// and omit it when the upstream value is zero.
func TestCheckFileInfo_LastModifiedTime_FromUpdatedAt(t *testing.T) {
	docID := uuid.New().String()
	updated := time.Date(2026, 5, 25, 13, 45, 30, 0, time.UTC)
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{
		ID:        docID,
		UpdatedAt: updated,
	}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	info, err := svc.CheckFileInfo(context.Background(), makeToken(docID, "read"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.LastModifiedTime != "2026-05-25T13:45:30Z" {
		t.Errorf("LastModifiedTime = %q, want 2026-05-25T13:45:30Z", info.LastModifiedTime)
	}
}

func TestCheckFileInfo_LastModifiedTime_OmittedWhenZero(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID} // UpdatedAt zero

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	info, err := svc.CheckFileInfo(context.Background(), makeToken(docID, "read"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.LastModifiedTime != "" {
		t.Errorf("LastModifiedTime = %q, want empty for zero UpdatedAt", info.LastModifiedTime)
	}
}

// TestCheckFileInfo_ReadOnly_MirrorsUserCanWrite ensures ReadOnly is the
// explicit inverse of UserCanWrite — important because Collabora reads
// both fields when deciding whether to render edit toolbars.
func TestCheckFileInfo_ReadOnly_MirrorsUserCanWrite(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

	rw, _ := svc.CheckFileInfo(context.Background(), makeToken(docID, "read,write"))
	if rw.ReadOnly {
		t.Error("ReadOnly must be false when token has write permission")
	}

	ro, _ := svc.CheckFileInfo(context.Background(), makeToken(docID, "read"))
	if !ro.ReadOnly {
		t.Error("ReadOnly must be true when token lacks write permission")
	}
}

func TestCheckFileInfo_PostMessageOrigin(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID}

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://app.alkem.io", zap.NewNop())
	info, err := svc.CheckFileInfo(context.Background(), makeToken(docID, "read"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.PostMessageOrigin != "https://app.alkem.io" {
		t.Errorf("PostMessageOrigin = %q, want https://app.alkem.io", info.PostMessageOrigin)
	}
}

func TestCheckFileInfo_DocumentNotFound(t *testing.T) {
	svc := NewWOPIService(newMockFileService(), newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken("nonexistent", "read")

	_, err := svc.CheckFileInfo(context.Background(), token)
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Errorf("expected ErrDocumentNotFound, got %v", err)
	}
}

func TestGetFile_Success(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID, ExternalID: "ext-abc"}
	fileSvc.files[docID] = []byte("file content here")

	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID, ExternalID: "old-ext"}
	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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
	svc := NewWOPIService(newMockFileService(), newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	_, err := svc.PutFile(context.Background(), token, "lock-B", strings.NewReader("content"))
	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Errorf("expected LockConflictError, got %v", err)
	}
}

func TestPutFile_LockMatch(t *testing.T) {
	docID := uuid.New().String()
	fileSvc := newMockFileService()
	fileSvc.docs[docID] = &model.Document{ID: docID}

	lockRepo := newMockLockRepo()
	lockRepo.locks[docID] = &model.Lock{
		FileID:    docID,
		LockID:    "lock-A",
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}

	svc := NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken(docID, "read,write")

	result, err := svc.PutFile(context.Background(), token, "lock-A", strings.NewReader("content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version == "" {
		t.Error("expected non-empty version")
	}
}

// --- Additional error path tests ---

func TestGetFile_DocumentNotFound(t *testing.T) {
	fileSvc := newMockFileService()
	// No doc in fileSvc.docs
	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken("missing-doc", "read")

	_, err := svc.GetFile(context.Background(), token)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestCheckFileInfo_ErrorFromRepo(t *testing.T) {
	fileSvc := &errorFileService{err: context.DeadlineExceeded}
	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken("doc-1", "read")

	_, err := svc.CheckFileInfo(context.Background(), token)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetFile_ErrorFromReadFile(t *testing.T) {
	fileSvc := newMockFileService()
	fileSvc.docs["doc-1"] = &model.Document{ID: "doc-1"}
	// ReadFile will fail because no file for doc-1
	svc := NewWOPIService(fileSvc, newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken("doc-1", "read")

	_, err := svc.GetFile(context.Background(), token)
	if err == nil {
		t.Error("expected error from ReadFile")
	}
}

func TestPutFile_ErrorFromLockRepo(t *testing.T) {
	fileSvc := newMockFileService()
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}
	svc := NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	token := makeToken("doc-1", "read,write")

	_, err := svc.PutFile(context.Background(), token, "", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error from lock repo")
	}
}

func TestLock_ErrorFromRepo(t *testing.T) {
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}
	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

	err := svc.Lock(context.Background(), "doc-1", "lock-1")
	if err == nil {
		t.Error("expected error from lock repo")
	}
}

func TestUnlock_ErrorFromRepo(t *testing.T) {
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}
	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

	err := svc.Unlock(context.Background(), "doc-1", "lock-1")
	if err == nil {
		t.Error("expected error from lock repo")
	}
}

func TestRefreshLock_ErrorFromRepo(t *testing.T) {
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}
	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

	err := svc.RefreshLock(context.Background(), "doc-1", "lock-1")
	if err == nil {
		t.Error("expected error from lock repo")
	}
}

func TestUnlockAndRelock_ErrorFromRepo(t *testing.T) {
	lockRepo := &errorLockRepo{err: context.DeadlineExceeded}
	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

	err := svc.UnlockAndRelock(context.Background(), "doc-1", "new", "old")
	if err == nil {
		t.Error("expected error from lock repo")
	}
}

func TestRefreshLock_NoLock(t *testing.T) {
	svc := NewWOPIService(newMockFileService(), newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	err := svc.RefreshLock(context.Background(), "doc-1", "lock-1")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}

func TestUnlockAndRelock_NoLock(t *testing.T) {
	svc := NewWOPIService(newMockFileService(), newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	err := svc.UnlockAndRelock(context.Background(), "doc-1", "new", "old")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}

// --- Error-producing mocks ---

type errorFileService struct {
	err error
}

func (e *errorFileService) FindByID(_ context.Context, _ string) (*model.Document, error) {
	return nil, e.err
}
func (e *errorFileService) ReadFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, e.err
}
func (e *errorFileService) WriteFile(_ context.Context, _ string, _ io.Reader) (*port.FileWriteResult, error) {
	return nil, e.err
}
func (e *errorFileService) FileExists(_ context.Context, _ string) (bool, error) {
	return false, e.err
}

type errorLockRepo struct {
	err error
}

func (e *errorLockRepo) Create(_ context.Context, _ *model.Lock) error { return e.err }
func (e *errorLockRepo) FindByFileID(_ context.Context, _ string) (*model.Lock, error) {
	return nil, e.err
}
func (e *errorLockRepo) UpdateLockID(_ context.Context, _, _, _ string, _ model.Lock) error {
	return e.err
}
func (e *errorLockRepo) RefreshExpiry(_ context.Context, _, _ string, _ *model.Lock) error {
	return e.err
}
func (e *errorLockRepo) DeleteByFileID(_ context.Context, _, _ string) error { return e.err }
func (e *errorLockRepo) DeleteExpired(_ context.Context) (int64, error)      { return 0, e.err }

// --- Lock operation tests (US2) ---

func TestLock_Acquire(t *testing.T) {
	docID := uuid.New().String()
	lockRepo := newMockLockRepo()
	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())

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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	err := svc.Unlock(context.Background(), docID, "lock-B")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}

func TestUnlock_NoLock(t *testing.T) {
	svc := NewWOPIService(newMockFileService(), newMockLockRepo(), "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
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

	svc := NewWOPIService(newMockFileService(), lockRepo, "https://wopi.example.com", "https://wopi.example.com", zap.NewNop())
	err := svc.UnlockAndRelock(context.Background(), docID, "new-lock", "wrong-old")

	var conflictErr *LockConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected LockConflictError, got %v", err)
	}
}
