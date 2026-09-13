package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// maxActiveRenders bounds concurrent Collabora conversions.
const maxActiveRenders = 2

// resolveMissMaxAttempts bounds resolveMiss's re-entry loop — never an
// unbounded spin against one frozen source-date observation.
const resolveMissMaxAttempts = 3

// RenderTimeout bounds one admitted render job — WOPI's shared HTTP write
// timeout, not a preview-specific setting. A var so tests can shorten it.
var RenderTimeout = 60 * time.Second

// Errors returned by PreviewService.Resolve.
var (
	ErrRenderAdmissionFull = errors.New("render admission queue is full")
	ErrRenderFailed        = errors.New("preview render failed")
)

// PreviewResult is Resolve's result; Body is nil iff NotModified.
type PreviewResult struct {
	ETag        string
	Body        io.ReadCloser
	NotModified bool
}

// PreviewService resolves an authorized, cached Collabora preview.
type PreviewService struct {
	fileSvc  port.FileService
	authSvc  port.AuthService
	cache    port.PreviewCacheRepository
	renderer port.ThumbnailRenderer
	flight   singleflight.Group
	slots    *semaphore.Weighted // combined active+waiting admission
	active   *semaphore.Weighted // at most maxActiveRenders concurrent
	logger   *zap.Logger
}

// NewPreviewService creates a PreviewService; waitingCapacity is the render
// admission queue's waiting capacity beyond maxActiveRenders.
func NewPreviewService(fileSvc port.FileService, authSvc port.AuthService, cache port.PreviewCacheRepository,
	renderer port.ThumbnailRenderer, waitingCapacity int, logger *zap.Logger) *PreviewService {
	return &PreviewService{
		fileSvc: fileSvc, authSvc: authSvc, cache: cache, renderer: renderer,
		slots: semaphore.NewWeighted(int64(maxActiveRenders + waitingCapacity)), active: semaphore.NewWeighted(maxActiveRenders),
		logger: logger,
	}
}

// Resolve authorizes actorID against sourceID's CURRENT read policy before
// any other outcome, including a 304.
func (s *PreviewService) Resolve(ctx context.Context, actorID, sourceID, ifNoneMatch string) (*PreviewResult, error) {
	src, err := s.fileSvc.FindByID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("lookup source: %w", err)
	}
	if src == nil {
		return nil, ErrDocumentNotFound
	}

	auth, err := s.authSvc.CheckPrivilege(ctx, actorID, "read", src.AuthorizationPolicyID)
	if err != nil {
		return nil, fmt.Errorf("check read privilege: %w", err)
	}
	if !auth.Allowed {
		return nil, ErrNotAuthorized
	}

	if src.Size == 0 {
		return nil, model.ErrUnsupportedPreviewSource
	}
	ext, err := model.PreviewExtension(src.MimeType)
	if err != nil {
		return nil, err
	}

	etag := etagFor(src.UpdatedAt)
	if ifNoneMatch != "" && ifNoneMatch == etag {
		return &PreviewResult{ETag: etag, NotModified: true}, nil
	}

	if entry, err := s.currentMapping(ctx, sourceID, src.UpdatedAt); err != nil {
		return nil, err
	} else if entry != nil {
		if body, ferr := s.fileSvc.ReadFile(ctx, entry.PreviewFileID); ferr == nil {
			return &PreviewResult{ETag: etag, Body: body}, nil
		}
		// A 404'd preview file is a miss, repaired below — no separate branch.
	}

	entry, err := s.resolveMiss(ctx, sourceID, ext, src.UpdatedAt)
	if err != nil {
		return nil, err
	}
	body, err := s.fileSvc.ReadFile(ctx, entry.PreviewFileID)
	if err != nil {
		return nil, fmt.Errorf("read committed preview: %w", err)
	}
	return &PreviewResult{ETag: etagFor(entry.SourceUpdatedDate), Body: body}, nil
}

// currentMapping returns sourceID's mapping when its date matches
// currentDate, else nil.
func (s *PreviewService) currentMapping(ctx context.Context, sourceID string, currentDate time.Time) (*model.PreviewCacheEntry, error) {
	entry, err := s.cache.FindBySourceID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("lookup preview cache: %w", err)
	}
	if entry != nil && entry.SourceUpdatedDate.Equal(currentDate) {
		return entry, nil
	}
	return nil, nil
}

// resolveMiss collapses concurrent misses via single-flight; a stale result
// re-enters against a freshly re-read date, bounded by resolveMissMaxAttempts.
func (s *PreviewService) resolveMiss(ctx context.Context, sourceID, extension string, observedDate time.Time) (*model.PreviewCacheEntry, error) {
	for attempt := 1; ; attempt++ {
		ch := s.flight.DoChan(sourceID, func() (interface{}, error) {
			return s.renderAndSwap(sourceID, extension)
		})
		select {
		case res := <-ch:
			if res.Err != nil {
				return nil, res.Err
			}
			entry := res.Val.(*model.PreviewCacheEntry)
			if !entry.SourceUpdatedDate.Before(observedDate) || attempt >= resolveMissMaxAttempts {
				return entry, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		src, err := s.fileSvc.FindByID(ctx, sourceID)
		if err != nil {
			return nil, fmt.Errorf("re-observe source: %w", err)
		}
		if src == nil {
			return nil, ErrDocumentNotFound
		}
		observedDate = src.UpdatedAt
	}
}

// renderAndSwap is the single-flight leader for one source miss: admit,
// recheck the cache, render, store, and atomically swap the mapping — the
// same path a 404'd preview file repairs through.
func (s *PreviewService) renderAndSwap(sourceID, extension string) (*model.PreviewCacheEntry, error) {
	if !s.slots.TryAcquire(1) {
		return nil, ErrRenderAdmissionFull
	}
	defer s.slots.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), RenderTimeout)
	defer cancel()

	if err := s.active.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRenderAdmissionFull, err)
	}
	defer s.active.Release(1)

	src, err := s.fileSvc.FindByID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("re-lookup source: %w", err)
	}
	if src == nil {
		return nil, ErrDocumentNotFound
	}
	// Recheck must also confirm the preview file still reads back, or a
	// mapping stuck on a 404'd file would short-circuit on itself.
	if existing, err := s.currentMapping(ctx, sourceID, src.UpdatedAt); err == nil && existing != nil {
		if ok, err := s.fileSvc.FileExists(ctx, existing.PreviewFileID); err == nil && ok {
			return existing, nil
		}
	}

	content, err := s.fileSvc.ReadFile(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("%w: read source: %w", ErrRenderFailed, err)
	}
	defer func() { _ = content.Close() }()

	png, err := s.renderer.Render(ctx, extension, content)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRenderFailed, err)
	}
	defer func() { _ = png.Close() }()

	previewID, err := s.fileSvc.CreatePreviewFile(ctx, src.StorageBucketID, png)
	if err != nil {
		return nil, fmt.Errorf("%w: store preview: %w", ErrRenderFailed, err)
	}
	return s.commitMapping(ctx, sourceID, previewID, src.UpdatedAt)
}

// commitMapping atomically swaps the mapping to the newly rendered preview.
// It never deletes the file it superseded: a concurrent reader may already
// hold that row and be mid-stream (Resolve reads the row and its file as
// two steps), so deleting here would race it into a 404 or a truncated
// read — the same accepted bucket-lifecycle garbage private-preview-file.md
// already tolerates for a best-effort delete that fails or is skipped.
func (s *PreviewService) commitMapping(ctx context.Context, sourceID, previewID string, renderedDate time.Time) (*model.PreviewCacheEntry, error) {
	entry := model.PreviewCacheEntry{SourceFileID: sourceID, PreviewFileID: previewID, SourceUpdatedDate: renderedDate}
	if err := s.cache.Upsert(ctx, entry); err != nil {
		return nil, fmt.Errorf("%w: commit mapping: %w", ErrRenderFailed, err)
	}
	return &entry, nil
}

// etagFor derives an ETag from updatedDate only — never storage identity.
func etagFor(t time.Time) string {
	return `"` + t.UTC().Format(time.RFC3339Nano) + `"`
}
