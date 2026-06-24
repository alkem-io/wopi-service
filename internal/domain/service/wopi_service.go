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
	fileSvc           port.FileService
	lockRepo          port.LockRepository
	baseURL           string
	postMessageOrigin string
	maxLockLifetime   time.Duration
	logger            *zap.Logger
}

// NewWOPIService creates a new WOPIService.
//
// postMessageOrigin is the embedding page's origin (scheme://host[:port]);
// Collabora uses it as the `PostMessageOrigin` target for save/connect
// status notifications back to the host frame. Empty disables the field.
//
// maxLockLifetime is the hard upper bound on how long a single Collabora
// lockID is allowed to persist via repeated refreshes. When a NEW lockID
// requests Lock on a file whose existing lock has lived past this, the
// new lockID is permitted to take over (zombie-defence). Zero or negative
// disables the defence — same-lockID refreshes can extend the lock
// indefinitely, which is the legacy behaviour.
func NewWOPIService(
	fileSvc port.FileService,
	lockRepo port.LockRepository,
	baseURL string,
	postMessageOrigin string,
	maxLockLifetime time.Duration,
	logger *zap.Logger,
) *WOPIService {
	return &WOPIService{
		fileSvc:           fileSvc,
		lockRepo:          lockRepo,
		baseURL:           baseURL,
		postMessageOrigin: postMessageOrigin,
		maxLockLifetime:   maxLockLifetime,
		logger:            logger,
	}
}

// CheckFileInfo returns WOPI file metadata for a document.
//
// WOPI spec invariants this implementation upholds:
//
//   - `OwnerId` MUST be stable per-file across all callers. We use the
//     document's CreatedBy when present; otherwise the document ID (also
//     stable per file). The previous implementation set this to the
//     calling actor, which broke Collabora's DocBroker state whenever a
//     second user opened the same file and observed a different owner.
//   - `UserId` is per-caller and distinguishes co-editors.
//   - `LastModifiedTime` is ISO 8601; we emit it only when file-service
//     reports a non-zero update timestamp (the field is optional).
//   - `ReadOnly` is the explicit inverse of `UserCanWrite` so the editor
//     reflects the token's permissions without inferring from defaults.
//   - `PostMessageOrigin` enables Collabora to post save/connection
//     status messages back to the embedding host frame.
func (s *WOPIService) CheckFileInfo(ctx context.Context, token *model.AccessToken) (*model.FileInfo, error) {
	doc, err := s.fileSvc.FindByID(ctx, token.FileID)
	if err != nil {
		return nil, fmt.Errorf("lookup document: %w", err)
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	canWrite := token.HasPermission("write")

	ownerID := doc.CreatedBy
	if ownerID == "" {
		ownerID = doc.ID
	}

	var lastModified string
	if !doc.UpdatedAt.IsZero() {
		lastModified = doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return &model.FileInfo{
		BaseFileName:            doc.DisplayName,
		OwnerID:                 ownerID,
		Size:                    doc.Size,
		UserID:                  token.ActorID,
		UserFriendlyName:        token.ActorName,
		Version:                 doc.ExternalID,
		UserCanWrite:            canWrite,
		SupportsLocks:           true,
		SupportsUpdate:          canWrite,
		UserCanNotWriteRelative: true,
		ReadOnly:                !canWrite,
		LastModifiedTime:        lastModified,
		PostMessageOrigin:       s.postMessageOrigin,
	}, nil
}

// GetFile retrieves file content from file-service by document ID.
func (s *WOPIService) GetFile(ctx context.Context, token *model.AccessToken) (io.ReadCloser, error) {
	content, err := s.fileSvc.ReadFile(ctx, token.FileID)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return content, nil
}

// PutFileResult holds the result of a PutFile operation. LastModifiedTime
// is the authoritative timestamp Collabora records for the saved version;
// it MUST be returned in the JSON body of a successful PutFile or
// Collabora treats the response as invalid and tears down the kit
// session (EPIPE → DocBroker unload → next open rejected).
type PutFileResult struct {
	Version          string
	LastModifiedTime time.Time
}

// PutFile saves updated file content via file-service.
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

	// LastModifiedTime is sampled at successful-write time. file-service
	// does not currently return an authoritative timestamp from the
	// store-and-link response, and millisecond-level skew against its
	// internal updated_date is acceptable — Collabora only needs the
	// value to be monotonically increasing across saves of the same file.
	return &PutFileResult{
		Version:          result.ExternalID,
		LastModifiedTime: time.Now().UTC(),
	}, nil
}

// Lock acquires, refreshes, or takes over a lock on a file.
//
// Branches:
//
//   - No existing (non-expired) lock → acquire fresh.
//   - Existing lock with the same lockID → refresh expiry.
//   - Existing lock with a different lockID, within MaxLockLifetime →
//     LockConflictError (the normal WOPI semantics: the lock belongs to
//     someone else, the caller must back off).
//   - Existing lock with a different lockID, **past MaxLockLifetime** →
//     atomic takeover. This defends against zombie DocBrokers that keep
//     refreshing their lock indefinitely, blocking every new session for
//     the same file. The takeover resets created_at + expires_at; the
//     old lockID is forever discarded.
//
// Same-lockID refreshes are never capped — an actively held lock by a
// real session must be allowed to continue regardless of how long the
// session has lasted.
func (s *WOPIService) Lock(ctx context.Context, fileID, lockID string) error {
	if lockID == "" {
		return fmt.Errorf("lock ID must not be empty")
	}

	existing, err := s.lockRepo.FindByFileID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("check existing lock: %w", err)
	}

	now := time.Now()

	if existing != nil {
		if existing.LockID == lockID {
			existing.ExpiresAt = now.Add(model.DefaultLockDuration)
			return wrapStaleLock(s.lockRepo.RefreshExpiry(ctx, fileID, lockID, existing))
		}

		// Different lockID. If the existing lock is past the configured
		// maximum lifetime, treat its holder as a presumed-dead zombie
		// and let the new lockID take over. Otherwise honour normal WOPI
		// lock-conflict semantics.
		if s.maxLockLifetime > 0 && now.Sub(existing.CreatedAt) > s.maxLockLifetime {
			s.logger.Info("zombie-lock takeover",
				zap.String("fileId", fileID),
				zap.String("oldLockId", existing.LockID),
				zap.String("newLockId", lockID),
				zap.Duration("age", now.Sub(existing.CreatedAt)),
			)
			err := s.lockRepo.Takeover(ctx, fileID, existing.LockID, lockID,
				now, now.Add(model.DefaultLockDuration))
			if errors.Is(err, port.ErrStaleLock) {
				// Lost the takeover race — another caller won and the lock
				// now holds a different ID than the snapshot we read. Refetch
				// the current state so the conflict surfaced to Collabora
				// reflects what's actually in the database; returning the
				// stale snapshot would point Collabora's X-WOPI-Lock header
				// at a lockID that no longer exists.
				latest, findErr := s.lockRepo.FindByFileID(ctx, fileID)
				if findErr != nil {
					return fmt.Errorf("refetch lock after takeover race: %w", findErr)
				}
				if latest == nil {
					// Winner already released between our race-loss and the
					// refetch. Treat as an empty conflict so Collabora knows
					// to retry without an OldLock value.
					return &LockConflictError{ExistingLockID: ""}
				}
				return &LockConflictError{ExistingLockID: latest.LockID}
			}
			return err
		}

		return &LockConflictError{ExistingLockID: existing.LockID}
	}

	// No lock or expired — acquire new lock
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
	if newLockID == "" {
		return fmt.Errorf("new lock ID must not be empty")
	}
	if oldLockID == "" {
		return fmt.Errorf("old lock ID must not be empty")
	}

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
