package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// maxActiveRenders bounds concurrent Collabora conversions.
const maxActiveRenders = 2

// RenderTimeout bounds one admitted render job — WOPI's shared HTTP write
// timeout, not a preview-specific setting. A var so tests can shorten it.
var RenderTimeout = 60 * time.Second

// Errors returned by PreviewService.Resolve.
var (
	ErrRenderAdmissionFull = errors.New("render admission queue is full")
	ErrRenderFailed        = errors.New("preview render failed")
	// ErrStaleSharedRender means the joined single-flight result predates the
	// source date this request observed. The caller gets 503 with no pixels; a
	// later ordinary request renders the newer state. There is no re-entry
	// loop and no automatic retry (ADR 0013, 2026-09-25).
	ErrStaleSharedRender = errors.New("shared render predates the observed source state")
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
// any other outcome, including a 304. Keys use canonical src.ID, not the
// caller's path spelling (sec-wopi-2: uuid.Parse accepts many spellings).
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

	if entry, err := s.currentMapping(ctx, src.ID, src.UpdatedAt); err != nil {
		return nil, err
	} else if entry != nil {
		if body, ferr := s.fileSvc.ReadFile(ctx, entry.PreviewFileID); ferr == nil {
			return &PreviewResult{ETag: etag, Body: body}, nil
		} else if !errors.Is(ferr, fs.ErrNotExist) {
			return nil, fmt.Errorf("read cached preview: %w", ferr)
		}
		// A 404'd preview file is a miss, repaired below — no separate branch.
	}

	entry, err := s.resolveMiss(ctx, src.ID, ext, src.UpdatedAt)
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

// resolveMiss collapses concurrent misses via single-flight. A waiter joins
// exactly ONE shared result: if that result predates the date this request
// observed, it gets ErrStaleSharedRender (503, no pixels) rather than looping.
func (s *PreviewService) resolveMiss(ctx context.Context, sourceID, extension string, observedDate time.Time) (*model.PreviewCacheEntry, error) {
	ch := s.flight.DoChan(sourceID, func() (interface{}, error) {
		return s.renderAndStore(sourceID, extension)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		entry := res.Val.(*model.PreviewCacheEntry)
		if entry.SourceUpdatedDate.Before(observedDate) {
			return nil, ErrStaleSharedRender
		}
		return entry, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// writeTarget decides, BEFORE the renderer's one-pass stream is consumed, what
// this job will do with the pixels it is about to produce. It returns either a
// still-current cached entry to reuse (no render needed at all), or the preview
// file ID to replace in place — empty meaning no preview row exists yet and one
// must be created.
func (s *PreviewService) writeTarget(ctx context.Context, sourceID string, srcUpdatedAt time.Time) (*model.PreviewCacheEntry, string, error) {
	mapped, err := s.cache.FindBySourceID(ctx, sourceID)
	if err != nil {
		return nil, "", fmt.Errorf("lookup preview cache: %w", err)
	}
	if mapped == nil {
		return nil, "", nil
	}

	// Recheck with the SUPPORTED content GET: a job that filled the cache while
	// this one queued must be reused. Never HEAD — file-service registers GET
	// only on that path, so HEAD yields 405 and would be misread as absence.
	if mapped.SourceUpdatedDate.Equal(srcUpdatedAt) {
		body, rerr := s.fileSvc.ReadFile(ctx, mapped.PreviewFileID)
		if rerr == nil {
			_ = body.Close()
			return mapped, "", nil
		}
		if !errors.Is(rerr, fs.ErrNotExist) {
			// 405/5xx/timeout/conflict are upstream errors, never absence.
			return nil, "", fmt.Errorf("%w: recheck cached preview: %w", ErrRenderFailed, rerr)
		}
	}

	// Missing content is not a missing row: only the metadata API can say
	// whether the logical file still exists, and that is what decides PUT vs
	// POST. A create here would strand the row the mapping still points at.
	meta, merr := s.fileSvc.FindByID(ctx, mapped.PreviewFileID)
	if merr != nil {
		return nil, "", fmt.Errorf("%w: lookup preview metadata: %w", ErrRenderFailed, merr)
	}
	if meta == nil {
		return nil, "", nil
	}
	return nil, mapped.PreviewFileID, nil
}

// renderAndStore is the single-flight leader for one source miss: admit,
// decide the write target, render, write, commit. The write target is resolved
// BEFORE rendering because the renderer's PNG stream is one-pass and cannot be
// replayed — so a failed write is never recoverable by creating a file instead.
// A present preview row (even one with missing content) is refreshed in place
// under its stable fileID; only a genuine metadata 404, or no mapping at all,
// creates one.
func (s *PreviewService) renderAndStore(sourceID, extension string) (*model.PreviewCacheEntry, error) {
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

	reuse, refreshID, err := s.writeTarget(ctx, sourceID, src.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if reuse != nil {
		return reuse, nil
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

	if refreshID != "" {
		if _, werr := s.fileSvc.WriteFile(ctx, refreshID, png); werr != nil {
			// Includes a 409: the guarded update lost a race or the row was
			// deleted. Never a licence to create a second preview file, and the
			// one-pass stream is spent anyway. Leave the date stale.
			return nil, fmt.Errorf("%w: replace preview content: %w", ErrRenderFailed, werr)
		}
		return s.commitRefresh(ctx, sourceID, refreshID, src.UpdatedAt)
	}

	previewID, err := s.fileSvc.CreatePreviewFile(ctx, src.StorageBucketID, png)
	if err != nil {
		return nil, fmt.Errorf("%w: store preview: %w", ErrRenderFailed, err)
	}
	return s.commitMapping(ctx, sourceID, previewID, src.UpdatedAt)
}

// commitRefresh advances ONLY source_updated_date, keeping preview_file_id.
// The preview file pre-existed this render, so a failed mapping write must
// never delete it: the date simply stays stale and a later request re-renders
// into the same row.
func (s *PreviewService) commitRefresh(ctx context.Context, sourceID, previewID string, renderedDate time.Time) (*model.PreviewCacheEntry, error) {
	entry := model.PreviewCacheEntry{SourceFileID: sourceID, PreviewFileID: previewID, SourceUpdatedDate: renderedDate}
	if err := s.cache.Upsert(ctx, entry); err != nil {
		return nil, fmt.Errorf("%w: advance mapping date: %w", ErrRenderFailed, err)
	}
	return &entry, nil
}

// commitMapping publishes a NEWLY CREATED preview file's id and date together.
// It runs only on first creation or genuine repair, never on an ordinary
// refresh. A failed commit is the one case where deleting IS safe: no mapping
// row ever referenced previewID, so no reader can hold it. Without this the
// file is orphaned on every failed attempt.
func (s *PreviewService) commitMapping(ctx context.Context, sourceID, previewID string, renderedDate time.Time) (*model.PreviewCacheEntry, error) {
	entry := model.PreviewCacheEntry{SourceFileID: sourceID, PreviewFileID: previewID, SourceUpdatedDate: renderedDate}
	if err := s.cache.Upsert(ctx, entry); err != nil {
		if derr := s.fileSvc.DeletePreviewFile(ctx, previewID); derr != nil {
			s.logger.Warn("uncommitted preview file left behind",
				zap.String("fileID", previewID), zap.Error(derr))
		}
		return nil, fmt.Errorf("%w: commit mapping: %w", ErrRenderFailed, err)
	}
	return &entry, nil
}

// etagFor derives an ETag from updatedDate only — never storage identity.
func etagFor(t time.Time) string {
	return `"` + t.UTC().Format(time.RFC3339Nano) + `"`
}
