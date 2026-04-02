package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// wrapStaleLock converts ErrStaleLock from the repository layer into a
// LockConflictError so the handler returns 409 instead of 500.
func wrapStaleLock(err error) error {
	if err != nil && errors.Is(err, port.ErrStaleLock) {
		return &LockConflictError{ExistingLockID: ""}
	}
	return err
}

// WOPIService implements the core WOPI use cases.
type WOPIService struct {
	fileSvc  port.FileService
	lockRepo port.LockRepository
	baseURL  string
	logger   *zap.Logger
}

// NewWOPIService creates a new WOPIService.
func NewWOPIService(
	fileSvc port.FileService,
	lockRepo port.LockRepository,
	baseURL string,
	logger *zap.Logger,
) *WOPIService {
	return &WOPIService{
		fileSvc:  fileSvc,
		lockRepo: lockRepo,
		baseURL:  baseURL,
		logger:   logger,
	}
}

// CheckFileInfo returns WOPI file metadata for a document.
func (s *WOPIService) CheckFileInfo(ctx context.Context, token *model.AccessToken) (*model.FileInfo, error) {
	doc, err := s.fileSvc.FindByID(ctx, token.FileID)
	if err != nil {
		return nil, fmt.Errorf("lookup document: %w", err)
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	canWrite := token.HasPermission("write")

	return &model.FileInfo{
		BaseFileName:            doc.DisplayName,
		OwnerID:                 token.ActorID,
		Size:                    doc.Size,
		UserID:                  token.ActorID,
		Version:                 doc.ExternalID,
		UserCanWrite:            canWrite,
		SupportsLocks:           true,
		SupportsUpdate:          canWrite,
		UserCanNotWriteRelative: true,
	}, nil
}

// GetFile retrieves file content from file-service-go by document ID.
func (s *WOPIService) GetFile(ctx context.Context, token *model.AccessToken) (io.ReadCloser, error) {
	content, err := s.fileSvc.ReadFile(ctx, token.FileID)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return content, nil
}

// PutFileResult holds the result of a PutFile operation.
type PutFileResult struct {
	Version   string
	Timestamp string // ISO 8601 for X-COOL-WOPI-Timestamp
}

// PutFile saves updated file content via file-service-go.
func (s *WOPIService) PutFile(ctx context.Context, token *model.AccessToken, lockID string, content io.Reader) (*PutFileResult, error) {
	if !token.HasPermission("write") {
		return nil, ErrNotAuthorized
	}

	// Check lock if one exists
	existingLock, err := s.lockRepo.FindByFileID(ctx, token.FileID)
	if err != nil {
		return nil, fmt.Errorf("check lock: %w", err)
	}
	if existingLock != nil {
		if lockID == "" || existingLock.LockID != lockID {
			return nil, &LockConflictError{ExistingLockID: existingLock.LockID}
		}
	}

	result, err := s.fileSvc.WriteFile(ctx, token.FileID, content)
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return &PutFileResult{
		Version:   result.ExternalID,
		Timestamp: "", // Will be set by handler from current time
	}, nil
}

// Lock acquires or refreshes a lock on a file.
func (s *WOPIService) Lock(ctx context.Context, fileID, lockID string) error {
	if lockID == "" {
		return fmt.Errorf("lock ID must not be empty")
	}

	existing, err := s.lockRepo.FindByFileID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("check existing lock: %w", err)
	}

	if existing != nil {
		if existing.LockID == lockID {
			existing.ExpiresAt = time.Now().Add(model.DefaultLockDuration)
			return wrapStaleLock(s.lockRepo.RefreshExpiry(ctx, fileID, lockID, existing))
		}
		return &LockConflictError{ExistingLockID: existing.LockID}
	}

	// No lock or expired — acquire new lock
	now := time.Now()
	lock := &model.Lock{
		ID:        uuid.New(),
		FileID:    fileID,
		LockID:    lockID,
		ExpiresAt: now.Add(model.DefaultLockDuration),
		CreatedAt: now,
	}
	return s.lockRepo.Create(ctx, lock)
}

// Unlock releases a lock on a file.
func (s *WOPIService) Unlock(ctx context.Context, fileID, lockID string) error {
	existing, err := s.lockRepo.FindByFileID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("check existing lock: %w", err)
	}

	if existing == nil {
		return &LockConflictError{ExistingLockID: ""}
	}
	if existing.LockID != lockID {
		return &LockConflictError{ExistingLockID: existing.LockID}
	}

	return wrapStaleLock(s.lockRepo.DeleteByFileID(ctx, fileID, lockID))
}

// RefreshLock extends the expiry of an existing lock.
func (s *WOPIService) RefreshLock(ctx context.Context, fileID, lockID string) error {
	existing, err := s.lockRepo.FindByFileID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("check existing lock: %w", err)
	}

	if existing == nil {
		return &LockConflictError{ExistingLockID: ""}
	}
	if existing.LockID != lockID {
		return &LockConflictError{ExistingLockID: existing.LockID}
	}

	existing.ExpiresAt = time.Now().Add(model.DefaultLockDuration)
	return wrapStaleLock(s.lockRepo.RefreshExpiry(ctx, fileID, lockID, existing))
}

// UnlockAndRelock atomically replaces one lock with another.
func (s *WOPIService) UnlockAndRelock(ctx context.Context, fileID, newLockID, oldLockID string) error {
	existing, err := s.lockRepo.FindByFileID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("check existing lock: %w", err)
	}

	if existing == nil {
		return &LockConflictError{ExistingLockID: ""}
	}
	if existing.LockID != oldLockID {
		return &LockConflictError{ExistingLockID: existing.LockID}
	}

	newLock := model.Lock{ExpiresAt: time.Now().Add(model.DefaultLockDuration)}
	return wrapStaleLock(s.lockRepo.UpdateLockID(ctx, fileID, oldLockID, newLockID, newLock))
}
