package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const previewTestMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// --- fakes ---

// previewFakeFileService is an in-memory port.FileService for preview tests.
// It stores both source documents/content and created preview files in the
// same maps, mirroring how file-service holds both in one bucket.
type previewFakeFileService struct {
	mu           sync.Mutex
	docs         map[string]*model.Document
	files        map[string][]byte
	deleted      map[string]bool
	nextID       int32
	findErr      error
	createErr    error
	readErr      map[string]error
	readGates    map[string]chan struct{}
	readEntered  map[string]chan struct{}
	laggingDates map[string][]time.Time
	findCalls    map[string]int
}

func newPreviewFakeFileService() *previewFakeFileService {
	return &previewFakeFileService{
		docs:        make(map[string]*model.Document),
		files:       make(map[string][]byte),
		deleted:     make(map[string]bool),
		readErr:     make(map[string]error),
		readGates:   make(map[string]chan struct{}),
		readEntered: make(map[string]chan struct{}),
	}
}

// setLaggingDates makes FindByID(id) return dates[n-1] on its n-th call
// (clamped to the last entry once exhausted), simulating a load-balanced
// file-service replica set whose metadata reads are not read-your-writes
// consistent.
func (f *previewFakeFileService) setLaggingDates(id string, dates ...time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.laggingDates == nil {
		f.laggingDates = make(map[string][]time.Time)
		f.findCalls = make(map[string]int)
	}
	f.laggingDates[id] = dates
}

func (f *previewFakeFileService) findCallCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.findCalls[id]
}

// armReadGate makes the NEXT ReadFile(id) call park after it has been
// entered (signaled on the returned channel) but before it looks up the
// file's bytes, until release is called — deterministically reproducing a
// concurrent supersede landing while that read is still in flight over the
// network, with no sleeps.
func (f *previewFakeFileService) armReadGate(id string) (entered <-chan struct{}, release func()) {
	enteredCh := make(chan struct{})
	gate := make(chan struct{})
	f.mu.Lock()
	f.readGates[id] = gate
	f.readEntered[id] = enteredCh
	f.mu.Unlock()
	return enteredCh, func() { close(gate) }
}

func (f *previewFakeFileService) FindByID(_ context.Context, id string) (*model.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return nil, f.findErr
	}
	doc, ok := f.docs[id]
	if !ok {
		return nil, nil
	}
	cp := *doc
	if seq, ok := f.laggingDates[id]; ok {
		idx := f.findCalls[id]
		f.findCalls[id]++
		if idx >= len(seq) {
			idx = len(seq) - 1
		}
		cp.UpdatedAt = seq[idx]
	}
	return &cp, nil
}

func (f *previewFakeFileService) ReadFile(_ context.Context, id string) (io.ReadCloser, error) {
	f.mu.Lock()
	gate, entered := f.readGates[id], f.readEntered[id]
	delete(f.readGates, id)
	delete(f.readEntered, id)
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if gate != nil {
		<-gate
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.readErr[id]; ok {
		return nil, err
	}
	data, ok := f.files[id]
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *previewFakeFileService) WriteFile(_ context.Context, _ string, _ io.Reader) (*port.FileWriteResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *previewFakeFileService) FileExists(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[id]
	return ok, nil
}

func (f *previewFakeFileService) CreatePreviewFile(_ context.Context, _ string, content io.Reader) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	id := fmt.Sprintf("preview-%d", atomic.AddInt32(&f.nextID, 1))
	f.mu.Lock()
	f.files[id] = data
	f.mu.Unlock()
	return id, nil
}

func (f *previewFakeFileService) DeletePreviewFile(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, id)
	f.deleted[id] = true
	return nil
}

func (f *previewFakeFileService) setUpdatedAt(sourceID string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[sourceID].UpdatedAt = t
}

// previewFakeCache is an in-memory port.PreviewCacheRepository.
type previewFakeCache struct {
	mu   sync.Mutex
	rows map[string]model.PreviewCacheEntry
}

func newPreviewFakeCache() *previewFakeCache {
	return &previewFakeCache{rows: make(map[string]model.PreviewCacheEntry)}
}

func (c *previewFakeCache) FindBySourceID(_ context.Context, id string) (*model.PreviewCacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[id]
	if !ok {
		return nil, nil
	}
	cp := row
	return &cp, nil
}

func (c *previewFakeCache) Upsert(_ context.Context, entry model.PreviewCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[entry.SourceFileID] = entry
	return nil
}

// fakeRenderer is a configurable port.ThumbnailRenderer.
//   - block == nil: every call returns immediately.
//   - block != nil, blockFirstOnly == false: every call blocks until block is
//     closed or ctx is done (used to hold admitted jobs open).
//   - block != nil, blockFirstOnly == true: only the first call blocks.
type fakeRenderer struct {
	calls          int32
	seq            int32
	err            error
	block          chan struct{}
	blockFirstOnly bool
}

func (r *fakeRenderer) Render(ctx context.Context, _ string, content io.Reader) (io.ReadCloser, error) {
	n := atomic.AddInt32(&r.calls, 1)
	_, _ = io.Copy(io.Discard, content)
	if r.block != nil && (!r.blockFirstOnly || n == 1) {
		select {
		case <-r.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	seq := atomic.AddInt32(&r.seq, 1)
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf("PNG-%d", seq)))), nil
}

// previewFakeAuth is a concurrency-safe port.AuthService fake — the
// package's shared mockAuthSvc records call history unsynchronized, which
// is fine for its own single-goroutine tests but races under the
// concurrent access these preview tests exercise.
type previewFakeAuth struct {
	mu      sync.Mutex
	allowed bool
}

func (a *previewFakeAuth) CheckPrivilege(_ context.Context, _, _, _ string) (*port.AuthResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &port.AuthResult{Allowed: a.allowed}, nil
}

func newAuthorizedActor() *previewFakeAuth { return &previewFakeAuth{allowed: true} }

func newDeniedActor() *previewFakeAuth { return &previewFakeAuth{allowed: false} }

// --- tests ---

func TestPreviewService_Resolve_AuthDeniedBeforeMimeOrCache(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC()
	// Unsupported MIME AND a matching pre-existing (stale-proof) cache row —
	// neither should ever be reached because auth denial must win first.
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: "image/png", Size: 1, UpdatedAt: now}
	cache := newPreviewFakeCache()
	_ = cache.Upsert(context.Background(), model.PreviewCacheEntry{SourceFileID: "src", PreviewFileID: "p1", SourceUpdatedDate: now})
	files.files["p1"] = []byte("png")

	svc := NewPreviewService(files, newDeniedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", etagFor(now))
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("error = %v, want ErrNotAuthorized", err)
	}
}

func TestPreviewService_Resolve_SourceNotFound(t *testing.T) {
	files := newPreviewFakeFileService()
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "missing", "")
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("error = %v, want ErrDocumentNotFound", err)
	}
}

func TestPreviewService_Resolve_UnsupportedSourceType(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: "application/pdf", Size: 1, UpdatedAt: time.Now()}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if !errors.Is(err, model.ErrUnsupportedPreviewSource) {
		t.Fatalf("error = %v, want ErrUnsupportedPreviewSource", err)
	}
}

func TestPreviewService_Resolve_ZeroSizeSourceRejected(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, UpdatedAt: time.Now()}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if !errors.Is(err, model.ErrUnsupportedPreviewSource) {
		t.Fatalf("error = %v, want ErrUnsupportedPreviewSource for a zero-byte source", err)
	}
}

func TestPreviewService_Resolve_AnonymousActorOnAnonymouslyReadableSource(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.files["src"] = []byte("source")
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	// An anonymous actor is just another actorID string reaching the same
	// CheckPrivilege call — no preview-specific exception exists for it.
	res, err := svc.Resolve(context.Background(), "anonymous-actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve error for anonymous actor: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	if len(body) == 0 {
		t.Error("expected rendered preview bytes for an anonymous actor")
	}
}

func TestPreviewService_Resolve_MissRendersOnceAndCaches(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.files["src"] = []byte("source-bytes")
	renderer := &fakeRenderer{}
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if res.NotModified || res.Body == nil {
		t.Fatalf("expected a 200 body, got %+v", res)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "PNG-1" {
		t.Errorf("body = %q", body)
	}
	if res.ETag != etagFor(now) {
		t.Errorf("ETag = %q, want %q", res.ETag, etagFor(now))
	}
	if atomic.LoadInt32(&renderer.calls) != 1 {
		t.Errorf("renderer calls = %d, want 1", renderer.calls)
	}

	// A second request at the same source date must hit the cache, not render.
	res2, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve (2nd) error: %v", err)
	}
	body2, _ := io.ReadAll(res2.Body)
	if string(body2) != "PNG-1" {
		t.Errorf("2nd body = %q, want cached PNG-1 (no new render)", body2)
	}
	if atomic.LoadInt32(&renderer.calls) != 1 {
		t.Errorf("renderer calls after 2nd request = %d, want still 1 (cache hit)", renderer.calls)
	}
}

func TestPreviewService_Resolve_MatchingETagReturnsNotModified(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now}
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", etagFor(now))
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if !res.NotModified {
		t.Fatal("expected NotModified")
	}
	if atomic.LoadInt32(&renderer.calls) != 0 {
		t.Errorf("a 304 must not invoke Collabora, got %d calls", renderer.calls)
	}
}

func TestPreviewService_Resolve_RevokedActorWithMatchingETagIsForbiddenNot304(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now}
	svc := NewPreviewService(files, newDeniedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", etagFor(now))
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("error = %v, want ErrNotAuthorized (never a 304 for a revoked reader)", err)
	}
}

func TestPreviewService_Resolve_StaleDateRendersAgainAndSwapsMapping(t *testing.T) {
	files := newPreviewFakeFileService()
	t1 := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: t1, StorageBucketID: "bucket"}
	files.files["src"] = []byte("v1")
	renderer := &fakeRenderer{}
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); err != nil {
		t.Fatalf("initial Resolve error: %v", err)
	}
	firstEntry, _ := cache.FindBySourceID(context.Background(), "src")

	t2 := t1.Add(time.Minute)
	files.setUpdatedAt("src", t2)
	files.files["src"] = []byte("v2")

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve after save error: %v", err)
	}
	if res.ETag != etagFor(t2) {
		t.Errorf("ETag = %q, want %q", res.ETag, etagFor(t2))
	}
	if atomic.LoadInt32(&renderer.calls) != 2 {
		t.Errorf("renderer calls = %d, want 2 (one render per distinct source state)", renderer.calls)
	}

	secondEntry, _ := cache.FindBySourceID(context.Background(), "src")
	if secondEntry.PreviewFileID == firstEntry.PreviewFileID {
		t.Error("a re-render must create a NEW preview file, never update the old one in place")
	}
	// The superseded file is deliberately left alone on the request path: a
	// concurrent reader may still be resolving/streaming it (see
	// TestPreviewService_ReaderHoldingResolvedMappingStreamsThatRowsBytesDespiteConcurrentSwap),
	// and the contract already treats an undeleted superseded file as
	// accepted bucket-lifecycle garbage.
	if files.deleted[firstEntry.PreviewFileID] {
		t.Error("the superseded preview file must not be deleted from the request path")
	}
	if _, err := files.ReadFile(context.Background(), firstEntry.PreviewFileID); err != nil {
		t.Errorf("superseded preview file should still be readable, got: %v", err)
	}
}

// TestPreviewService_ReaderHoldingResolvedMappingStreamsThatRowsBytesDespiteConcurrentSwap
// proves the invariant from contracts/private-preview-file.md: a reader that
// has already resolved sourceID's mapping to one preview fileID/date still
// streams exactly that row's bytes even though its own file-service read is
// still in flight when a concurrent request fully re-renders and swaps the
// mapping to a new preview fileID. Gated on channels, not sleeps.
func TestPreviewService_ReaderHoldingResolvedMappingStreamsThatRowsBytesDespiteConcurrentSwap(t *testing.T) {
	files := newPreviewFakeFileService()
	t1 := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: t1, StorageBucketID: "bucket"}
	files.files["src"] = []byte("v1")
	cache := newPreviewFakeCache()
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	// Warm the cache with an ordinary cold render committing preview-1@t1.
	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); err != nil {
		t.Fatalf("warm-up Resolve error: %v", err)
	}
	firstEntry, _ := cache.FindBySourceID(context.Background(), "src")

	entered, release := files.armReadGate(firstEntry.PreviewFileID)

	type outcome struct {
		res *PreviewResult
		err error
	}
	resA := make(chan outcome, 1)
	go func() {
		r, err := svc.Resolve(context.Background(), "actor", "src", "")
		resA <- outcome{r, err}
	}()

	<-entered // A resolved the mapping row and is mid file-service read

	// A concurrent save fully re-renders and swaps the mapping to a brand
	// new preview file while A is still parked above.
	t2 := t1.Add(time.Minute)
	files.setUpdatedAt("src", t2)
	files.files["src"] = []byte("v2")
	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); err != nil {
		t.Fatalf("concurrent re-render Resolve error: %v", err)
	}
	secondEntry, _ := cache.FindBySourceID(context.Background(), "src")
	if secondEntry.PreviewFileID == firstEntry.PreviewFileID {
		t.Fatal("setup error: the mapping did not actually swap")
	}

	release() // let A's blocked read proceed

	oa := <-resA
	if oa.err != nil {
		t.Fatalf("A (holding the original mapping row) error: %v", oa.err)
	}
	body, err := io.ReadAll(oa.res.Body)
	if err != nil {
		t.Fatalf("A body read error: %v", err)
	}
	if string(body) != "PNG-1" {
		t.Errorf("A body = %q, want PNG-1 (exactly the bytes committed with its own resolved row)", body)
	}
	if oa.res.ETag != etagFor(t1) {
		t.Errorf("A ETag = %q, want %q (its own row's date, never the swapped-in one)", oa.res.ETag, etagFor(t1))
	}
}

// TestPreviewService_ResolveMissBoundsReEntryAgainstALaggingReplicaObservation
// proves resolveMiss cannot spin unboundedly when a load-balanced
// file-service replica set answers metadata reads inconsistently: Resolve's
// own top-level read observes a newer date, but every later read (the
// render's re-lookup, and every re-observation inside resolveMiss) lands on
// a replica still reporting an older one. The render and file-service call
// counts must stay small and bounded, never grow into an unbounded spin.
func TestPreviewService_ResolveMissBoundsReEntryAgainstALaggingReplicaObservation(t *testing.T) {
	files := newPreviewFakeFileService()
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(time.Minute)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: t2, StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	// Call 1 (Resolve's own top-level read) observes t2; every call after
	// that lands on a lagging replica still reporting t1.
	files.setLaggingDates("src", t2, t1, t1, t1, t1, t1, t1, t1, t1, t1)
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := svc.Resolve(ctx, "actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if res.Body == nil {
		t.Fatal("expected a preview body despite the lagging replica")
	}
	if got := atomic.LoadInt32(&renderer.calls); got > resolveMissMaxAttempts {
		t.Errorf("renderer calls = %d, want at most %d (bounded re-entry, not an unbounded spin)", got, resolveMissMaxAttempts)
	}
	if got := files.findCallCount("src"); got > resolveMissMaxAttempts+2 {
		t.Errorf("file-service metadata reads = %d, want a small bounded number, not an unbounded spin", got)
	}
}

func TestPreviewService_Resolve_CachedFile404IsRepairedNotErrored(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	cache := newPreviewFakeCache()
	// A row whose date matches but whose file is gone (e.g. bucket cleanup).
	_ = cache.Upsert(context.Background(), model.PreviewCacheEntry{SourceFileID: "src", PreviewFileID: "ghost", SourceUpdatedDate: now})
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if res.Body == nil {
		t.Fatal("expected a repaired preview body")
	}
	if atomic.LoadInt32(&renderer.calls) != 1 {
		t.Errorf("renderer calls = %d, want 1 (repair render)", renderer.calls)
	}
}

func TestPreviewService_Resolve_InvalidCollaboraResponseFailsClosed(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: time.Now(), StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	renderer := &fakeRenderer{err: errors.New("collabora returned 500")}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("error = %v, want ErrRenderFailed", err)
	}
}

func TestPreviewService_Resolve_FileServiceCreateFailureFailsClosed(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: time.Now(), StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	files.createErr = errors.New("file-service unavailable")
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), &fakeRenderer{}, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("error = %v, want ErrRenderFailed", err)
	}
}

// TestPreviewService_DistinctSourcesNeverShareAPreviewFileID proves two
// sources in the same bucket whose renders happen to be byte-identical
// still receive distinct preview fileIDs (skipDedup=true), and that
// re-rendering one never touches the other's cached bytes.
func TestPreviewService_DistinctSourcesNeverShareAPreviewFileID(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["a"] = &model.Document{ID: "a", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.docs["b"] = &model.Document{ID: "b", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.files["a"] = []byte("identical")
	files.files["b"] = []byte("identical")
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	if _, err := svc.Resolve(context.Background(), "actor", "a", ""); err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), "actor", "b", ""); err != nil {
		t.Fatalf("resolve b: %v", err)
	}
	entryA, _ := cache.FindBySourceID(context.Background(), "a")
	entryB, _ := cache.FindBySourceID(context.Background(), "b")
	if entryA.PreviewFileID == entryB.PreviewFileID {
		t.Fatal("two distinct sources must never share one preview fileID")
	}

	// Re-render a's preview; b's mapping and bytes must be untouched.
	files.setUpdatedAt("a", now.Add(time.Minute))
	files.files["a"] = []byte("changed")
	if _, err := svc.Resolve(context.Background(), "actor", "a", ""); err != nil {
		t.Fatalf("re-resolve a: %v", err)
	}
	entryBAfter, _ := cache.FindBySourceID(context.Background(), "b")
	if entryBAfter.PreviewFileID != entryB.PreviewFileID {
		t.Fatal("superseding a's preview must not change b's mapping")
	}
	bBody, _ := files.ReadFile(context.Background(), entryBAfter.PreviewFileID)
	got, _ := io.ReadAll(bBody)
	if string(got) != "PNG-2" { // b was rendered 2nd overall (a=1, b=2, a-rerender=3)
		t.Errorf("b's cached bytes changed: %q", got)
	}
}

func TestPreviewService_ConcurrentColdRequestsCollapseToOneRender(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: time.Now().UTC().Truncate(time.Second), StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop())

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Resolve(context.Background(), "actor", "src", "")
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d error: %v", i, err)
		}
	}
	if atomic.LoadInt32(&renderer.calls) != 1 {
		t.Errorf("renderer calls = %d, want 1 (same-file misses must collapse)", renderer.calls)
	}
}

// TestPreviewService_WaiterObservingNewerDateReRenders proves that a
// waiter that sees a newer source date than the in-flight job's rendered
// date must never accept that older representation, and must itself
// trigger a fresh render once the stale job releases the single-flight key.
func TestPreviewService_WaiterObservingNewerDateReRenders(t *testing.T) {
	files := newPreviewFakeFileService()
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(time.Minute)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: t1, StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")

	gate := make(chan struct{})
	renderer := &fakeRenderer{block: gate, blockFirstOnly: true}
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	type outcome struct {
		res *PreviewResult
		err error
	}
	resA := make(chan outcome, 1)
	go func() {
		r, err := svc.Resolve(context.Background(), "actor", "src", "")
		resA <- outcome{r, err}
	}()

	// Wait for the leader to be admitted and blocked inside Render.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&renderer.calls) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("leader never started rendering")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// A save lands while the render is in flight.
	files.setUpdatedAt("src", t2)

	resB := make(chan outcome, 1)
	go func() {
		r, err := svc.Resolve(context.Background(), "actor", "src", "")
		resB <- outcome{r, err}
	}()
	time.Sleep(20 * time.Millisecond) // let B join the same in-flight key
	close(gate)                       // release the leader

	oa := <-resA
	if oa.err != nil {
		t.Fatalf("A error: %v", oa.err)
	}
	ob := <-resB
	if ob.err != nil {
		t.Fatalf("B error: %v", ob.err)
	}

	if oa.res.ETag == ob.res.ETag {
		t.Fatalf("A (%s) and B (%s) must not share an ETag", oa.res.ETag, ob.res.ETag)
	}
	if ob.res.ETag != etagFor(t2) {
		t.Errorf("B's ETag = %q, want %q (current source date, never the stale render's)", ob.res.ETag, etagFor(t2))
	}
	if atomic.LoadInt32(&renderer.calls) != 2 {
		t.Errorf("renderer calls = %d, want 2 (one per distinct source state)", renderer.calls)
	}
}

// TestPreviewService_JobSurvivesCallerDisconnect proves a disconnected
// browser stops waiting without cancelling the shared render/store job
// other waiters (and the cache) still depend on.
func TestPreviewService_JobSurvivesCallerDisconnect(t *testing.T) {
	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: time.Now().UTC().Truncate(time.Second), StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	gate := make(chan struct{})
	renderer := &fakeRenderer{block: gate}
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Resolve(ctx, "actor", "src", "")
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&renderer.calls) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("render never started")
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel() // simulate a browser disconnect

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnected caller should stop waiting immediately")
	}

	close(gate) // let the shared job finish

	waitDeadline := time.Now().Add(time.Second)
	for {
		entry, _ := cache.FindBySourceID(context.Background(), "src")
		if entry != nil {
			return
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("shared job did not complete/cache after caller disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPreviewService_DeadlineExpiryFailsTheRender(t *testing.T) {
	orig := RenderTimeout
	RenderTimeout = 30 * time.Millisecond
	defer func() { RenderTimeout = orig }()

	files := newPreviewFakeFileService()
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: time.Now(), StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	renderer := &fakeRenderer{block: make(chan struct{})} // never released
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("error = %v, want ErrRenderFailed on deadline expiry", err)
	}
}

// TestPreviewService_AdmissionBoundsElevenDistinctColdRequests proves
// With the default queue capacity, eleven simultaneous
// distinct cold requests produce at most two concurrent renders, up to
// eight admitted-but-waiting, and one immediate 503 — without an extra
// Collabora call for the rejected one.
func TestPreviewService_AdmissionBoundsElevenDistinctColdRequests(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	const n = 11
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("src-%d", i)
		files.docs[id] = &model.Document{ID: id, AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
		files.files[id] = []byte("content")
	}
	gate := make(chan struct{})
	renderer := &fakeRenderer{block: gate}
	svc := NewPreviewService(files, newAuthorizedActor(), newPreviewFakeCache(), renderer, 8, zap.NewNop()) // default capacity

	var wg sync.WaitGroup
	var done int32
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Resolve(context.Background(), "actor", fmt.Sprintf("src-%d", i), "")
			errs[i] = err
			atomic.AddInt32(&done, 1)
		}(i)
	}

	// Exactly two should be actively rendering; the rest are either waiting
	// on admission (blocked, not yet in Render) or already rejected.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&renderer.calls) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("expected two concurrent active renders")
		}
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let any excess concurrency show up, if it exists
	if got := atomic.LoadInt32(&renderer.calls); got != 2 {
		t.Fatalf("concurrent renders = %d, want exactly 2 (the hard cap)", got)
	}
	if got := atomic.LoadInt32(&done); got != 1 {
		t.Fatalf("completed-before-release = %d, want exactly 1 (the immediately rejected request)", got)
	}

	close(gate) // release everything admitted
	wg.Wait()

	var rejected, succeeded int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRenderAdmissionFull):
			rejected++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1", rejected)
	}
	if succeeded != n-1 {
		t.Errorf("succeeded = %d, want %d", succeeded, n-1)
	}
	if got := atomic.LoadInt32(&renderer.calls); got != n-1 {
		t.Errorf("total renders = %d, want %d (the rejected request never calls Collabora)", got, n-1)
	}
}

func TestPreviewService_CacheHitBypassesAdmissionEvenWhenFull(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["hit"] = &model.Document{ID: "hit", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now}
	files.files["preview-hit"] = []byte("cached")
	cache := newPreviewFakeCache()
	_ = cache.Upsert(context.Background(), model.PreviewCacheEntry{SourceFileID: "hit", PreviewFileID: "preview-hit", SourceUpdatedDate: now})

	// Zero waiting capacity, and both active slots pre-occupied by a
	// permanently blocked miss elsewhere — a cache hit must still succeed.
	gate := make(chan struct{})
	renderer := &fakeRenderer{block: gate}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 0, zap.NewNop())

	files.docs["a"] = &model.Document{ID: "a", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "b"}
	files.docs["b"] = &model.Document{ID: "b", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "b"}
	files.files["a"] = []byte("a")
	files.files["b"] = []byte("b")
	go func() { _, _ = svc.Resolve(context.Background(), "actor", "a", "") }()
	go func() { _, _ = svc.Resolve(context.Background(), "actor", "b", "") }()

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&renderer.calls) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("expected both active slots occupied")
		}
		time.Sleep(2 * time.Millisecond)
	}

	res, err := svc.Resolve(context.Background(), "actor", "hit", "")
	if err != nil {
		t.Fatalf("cache hit must bypass admission entirely, got error: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "cached" {
		t.Errorf("body = %q", body)
	}
	close(gate)
}
