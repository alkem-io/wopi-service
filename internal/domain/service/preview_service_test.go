package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	mu          sync.Mutex
	docs        map[string]*model.Document
	files       map[string][]byte
	deleted     map[string]bool
	nextID      int32
	findErr     error
	createErr   error
	readErr     map[string]error
	readGates   map[string]chan struct{}
	readEntered map[string]chan struct{}
	writes      map[string]int
	writeErr    map[string]error
	// spellingAliases mirrors file-service resolving a document ID with
	// uuid.Parse: case-insensitive, and accepting hyphen-less, urn:uuid:
	// and braced forms. Alternate spelling -> canonical document ID.
	spellingAliases map[string]string
}

func newPreviewFakeFileService() *previewFakeFileService {
	return &previewFakeFileService{
		docs:        make(map[string]*model.Document),
		files:       make(map[string][]byte),
		deleted:     make(map[string]bool),
		readErr:     make(map[string]error),
		readGates:   make(map[string]chan struct{}),
		readEntered: make(map[string]chan struct{}),
		writes:      make(map[string]int),
		writeErr:    make(map[string]error),

		spellingAliases: make(map[string]string),
	}
}

// addSpelling registers an alternate path spelling that file-service resolves
// to the same canonical document, as uuid.Parse does in production.
func (f *previewFakeFileService) addSpelling(alias, canonical string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spellingAliases[alias] = canonical
}

// canonicalise resolves an alternate spelling exactly as file-service does on
// EVERY endpoint, not just metadata. Callers must already hold f.mu.
func (f *previewFakeFileService) canonicalise(id string) string {
	if canonical, aliased := f.spellingAliases[id]; aliased {
		return canonical
	}
	return id
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
	id = f.canonicalise(id)
	doc, ok := f.docs[id]
	if !ok {
		return nil, nil
	}
	cp := *doc
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
	id = f.canonicalise(id)
	if err, ok := f.readErr[id]; ok {
		return nil, err
	}
	data, ok := f.files[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", fs.ErrNotExist, id)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// WriteFile replaces an EXISTING logical file's content in place, exactly as
// file-service's PUT does: the id and its (absent) authorization are preserved,
// and no new row is created. writeErr injects an upstream failure (405/5xx/409).
func (f *previewFakeFileService) WriteFile(_ context.Context, id string, content io.Reader) (*port.FileWriteResult, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if werr, ok := f.writeErr[id]; ok {
		return nil, werr
	}
	id = f.canonicalise(id)
	// Existence is a property of the LOGICAL row, not of its content: PUT must
	// succeed on a row whose bytes are missing, repairing it in place.
	if _, exists := f.docs[id]; !exists {
		return nil, fmt.Errorf("document not found: %s", id)
	}
	f.files[id] = data
	f.writes[id]++
	return &port.FileWriteResult{Size: int64(len(data))}, nil
}

// writeCount reports how many in-place replacements a preview file received.
func (f *previewFakeFileService) writeCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[id]
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
	// A create yields BOTH a logical row (metadata) and content, as
	// POST /internal/file does — the metadata row is what later decides
	// PUT-vs-POST, so a fake that only stores bytes would force create forever.
	f.files[id] = data
	f.docs[id] = &model.Document{ID: id, StorageBucketID: "bucket"}
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
	mu        sync.Mutex
	rows      map[string]model.PreviewCacheEntry
	upsertErr error
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
	if c.upsertErr != nil {
		return c.upsertErr
	}
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

func TestPreviewService_Resolve_StaleDateRendersAgainIntoTheSameFile(t *testing.T) {
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
	if secondEntry.PreviewFileID != firstEntry.PreviewFileID {
		t.Errorf("preview_file_id = %q, want the stable %q — a re-render replaces content in place, it never creates a second file",
			secondEntry.PreviewFileID, firstEntry.PreviewFileID)
	}
	if got := files.writeCount(firstEntry.PreviewFileID); got != 1 {
		t.Errorf("in-place replacements = %d, want 1", got)
	}
	// Nothing is superseded, so nothing is deleted: the row that the previous
	// render committed is the row this render just refreshed.
	if files.deleted[firstEntry.PreviewFileID] {
		t.Error("the preview file must never be deleted from the request path")
	}
	body, err := files.ReadFile(context.Background(), firstEntry.PreviewFileID)
	if err != nil {
		t.Fatalf("preview file should still be readable, got: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, _ := io.ReadAll(body)
	if string(got) != "PNG-2" {
		t.Errorf("preview bytes = %q, want PNG-2 (the newer render, under the same file ID)", got)
	}
}

// TestPreviewService_ReaderMidStreamSeesNewerPixelsUnderItsOlderETag pins the
// tradeoff that contracts/private-preview-file.md accepts explicitly, and that
// the stable-file design makes reachable: because a refresh replaces content
// under the SAME preview file ID, a reader whose file-service read is still in
// flight when that refresh lands streams the NEWER pixels while carrying the
// older ETag it resolved with. That is deliberate and bounded — a browser
// revalidating with that ETag gets the current one on its next request — and it
// is strictly better than the alternative it replaces, where every refresh
// leaked a second, never-reclaimed preview file. Gated on channels, not sleeps.
func TestPreviewService_ReaderMidStreamSeesNewerPixelsUnderItsOlderETag(t *testing.T) {
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

	// A concurrent save re-renders into the very file A is mid-read of.
	t2 := t1.Add(time.Minute)
	files.setUpdatedAt("src", t2)
	files.files["src"] = []byte("v2")
	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); err != nil {
		t.Fatalf("concurrent re-render Resolve error: %v", err)
	}
	secondEntry, _ := cache.FindBySourceID(context.Background(), "src")
	if secondEntry.PreviewFileID != firstEntry.PreviewFileID {
		t.Fatalf("setup error: the refresh should have reused %q, got %q",
			firstEntry.PreviewFileID, secondEntry.PreviewFileID)
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
	// The accepted tradeoff, asserted rather than left implicit: A streams the
	// refreshed bytes, not the ones current when it resolved.
	if string(body) != "PNG-2" {
		t.Errorf("A body = %q, want PNG-2 (the refreshed content of the same preview file)", body)
	}
	if oa.res.ETag != etagFor(t1) {
		t.Errorf("A ETag = %q, want %q (the date A itself resolved — never silently advanced mid-request)", oa.res.ETag, etagFor(t1))
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

// TestPreviewService_Resolve_CancelledCacheHitReadNeverRenders proves that a
// cancelled (or otherwise transient) read of an already-cached, current
// preview file is reported as an error, not silently repaired via a render —
// distinguishing it from the genuine-404 case above.
func TestPreviewService_Resolve_CancelledCacheHitReadNeverRenders(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC().Truncate(time.Second)
	files.docs["src"] = &model.Document{ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME, Size: 1, UpdatedAt: now, StorageBucketID: "bucket"}
	files.files["src"] = []byte("content")
	files.files["cached-preview"] = []byte("png")
	files.readErr["cached-preview"] = context.Canceled
	cache := newPreviewFakeCache()
	_ = cache.Upsert(context.Background(), model.PreviewCacheEntry{SourceFileID: "src", PreviewFileID: "cached-preview", SourceUpdatedDate: now})
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	_, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err == nil {
		t.Fatal("expected an error, not a repaired render")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want anything but fs.ErrNotExist", err)
	}
	if got := atomic.LoadInt32(&renderer.calls); got != 0 {
		t.Errorf("renderer calls = %d, want 0 (a cancelled cache-hit read must never trigger a render)", got)
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

// TestPreviewService_WaiterObservingNewerDateGetsNoPixels proves the adopted
// contract for the real case — a document saved while an older render is in
// flight. The waiter joins ONE shared result; because that result records a
// source date older than the one the waiter itself observed, it is refused
// with ErrStaleSharedRender (503, no pixels) rather than being served a
// representation it knows is stale or looping for a fresh one. The leader
// still gets its own result, and an ordinary later request renders the newer
// state. There is no attempt limit and no automatic client retry.
func TestPreviewService_WaiterObservingNewerDateGetsNoPixels(t *testing.T) {
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
	if oa.res.ETag != etagFor(t1) {
		t.Errorf("A's ETag = %q, want %q (the state A observed and rendered)", oa.res.ETag, etagFor(t1))
	}

	ob := <-resB
	if !errors.Is(ob.err, ErrStaleSharedRender) {
		t.Fatalf("B error = %v, want ErrStaleSharedRender", ob.err)
	}
	if ob.res != nil {
		t.Error("B must receive no pixels at all, not a stale representation")
	}
	if atomic.LoadInt32(&renderer.calls) != 1 {
		t.Errorf("renderer calls = %d, want 1 — a refused waiter must not loop into its own render", renderer.calls)
	}

	// The newer state is not lost: an ordinary later request renders it.
	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("later Resolve error: %v", err)
	}
	if res.ETag != etagFor(t2) {
		t.Errorf("later ETag = %q, want %q", res.ETag, etagFor(t2))
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

// TestPreviewService_FailedCommitDeletesTheUncommittedPreview guards the
// cleanup on the one path where deleting a preview file is race-free: the
// mapping commit failed, so no row ever referenced previewID and no reader can
// hold it. Without it each failed attempt orphans another private file.
func TestPreviewService_FailedCommitDeletesTheUncommittedPreview(t *testing.T) {
	files := newPreviewFakeFileService()
	now := time.Now().UTC()
	files.docs["src"] = &model.Document{
		ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME,
		Size: 1, StorageBucketID: "bucket", UpdatedAt: now,
	}
	files.files["src"] = []byte("source bytes")

	cache := newPreviewFakeCache()
	cache.upsertErr = errors.New("commit exploded")

	svc := NewPreviewService(files, newAuthorizedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("error = %v, want ErrRenderFailed", err)
	}

	files.mu.Lock()
	created := len(files.deleted)
	leaked := []string{}
	for id := range files.files {
		if id != "src" {
			leaked = append(leaked, id)
		}
	}
	files.mu.Unlock()

	if created == 0 {
		t.Error("no preview file was deleted; a failed commit orphaned it")
	}
	if len(leaked) != 0 {
		t.Errorf("uncommitted preview files left behind: %v", leaked)
	}
}

// TestPreviewService_ResolveKeysOnCanonicalIDNotPathSpelling is the regression
// guard for security finding sec-wopi-2. file-service resolves a document ID
// with uuid.Parse, which is case-insensitive and also accepts hyphen-less,
// urn:uuid: and braced forms — so one document has many valid path spellings.
// Keying the cache, the single-flight group or the mapping row on the caller's
// raw path parameter lets any reader defeat the cache entirely: N spellings
// produce N renders, N preview files and N rows against a single-replica,
// CPU-limited Collabora that also serves live editing.
func TestPreviewService_ResolveKeysOnCanonicalIDNotPathSpelling(t *testing.T) {
	const canonical = "01a09a51-e950-7411-a327-48ebc00debd7"
	spellings := []string{
		canonical,
		"01A09A51-E950-7411-A327-48EBC00DEBD7",          // upper case
		"01a09a51e9507411a32748ebc00debd7",              // hyphen-less
		"urn:uuid:01a09a51-e950-7411-a327-48ebc00debd7", // urn form
		"{01a09a51-e950-7411-a327-48ebc00debd7}",        // braced
	}

	files := newPreviewFakeFileService()
	now := time.Now().UTC()
	files.docs[canonical] = &model.Document{
		ID: canonical, AuthorizationPolicyID: "pol",
		MimeType: "application/vnd.oasis.opendocument.text", Size: 1,
		StorageBucketID: "bucket", UpdatedAt: now,
	}
	files.files[canonical] = []byte("source bytes")
	for _, s := range spellings[1:] {
		files.addSpelling(s, canonical)
	}

	cache := newPreviewFakeCache()
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	for _, spelling := range spellings {
		res, err := svc.Resolve(context.Background(), "actor", spelling, "")
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", spelling, err)
		}
		if res.Body != nil {
			_ = res.Body.Close()
		}
	}

	// One document, however it is spelled, is one render and one mapping row.
	if got := atomic.LoadInt32(&renderer.calls); got != 1 {
		t.Errorf("Collabora renders = %d across %d spellings of one document, want 1 "+
			"(a cache keyed on the caller's path spelling makes every spelling miss)",
			got, len(spellings))
	}
	cache.mu.Lock()
	rows := make([]string, 0, len(cache.rows))
	for k := range cache.rows {
		rows = append(rows, k)
	}
	cache.mu.Unlock()
	if len(rows) != 1 {
		t.Errorf("document_preview_cache rows = %d %v, want exactly 1 keyed on %q",
			len(rows), rows, canonical)
	} else if rows[0] != canonical {
		t.Errorf("cache row keyed on %q, want the canonical %q", rows[0], canonical)
	}
}

// --- stable-preview-file behaviour (ADR 0013, 2026-09-25) ---

// previewSource builds the "src" source document with saved bytes, ready to
// render.
func previewSource(files *previewFakeFileService, at time.Time) {
	files.docs["src"] = &model.Document{
		ID: "src", AuthorizationPolicyID: "pol", MimeType: previewTestMIME,
		Size: 1, StorageBucketID: "bucket", UpdatedAt: at,
	}
	files.files["src"] = []byte("source bytes")
}

// countPreviewFiles counts logical preview files, excluding the source itself.
func countPreviewFiles(files *previewFakeFileService, sourceID string) int {
	files.mu.Lock()
	defer files.mu.Unlock()
	n := 0
	for id := range files.files {
		if id != sourceID {
			n++
		}
	}
	return n
}

// An ordinary stale refresh must REPLACE the mapped preview's content under its
// existing fileID and advance only the date. Creating a second logical file on
// every refresh is the growth this design exists to remove (wopi-service#36).
func TestPreviewService_RefreshReplacesContentKeepingTheStableFileID(t *testing.T) {
	files := newPreviewFakeFileService()
	t0 := time.Now().UTC().Truncate(time.Second)
	previewSource(files, t0)
	cache := newPreviewFakeCache()
	renderer := &fakeRenderer{}
	svc := NewPreviewService(files, newAuthorizedActor(), cache, renderer, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	_ = res.Body.Close()
	first := cache.rows["src"].PreviewFileID

	for i := 1; i <= 3; i++ {
		files.setUpdatedAt("src", t0.Add(time.Duration(i)*time.Minute))
		r, rerr := svc.Resolve(context.Background(), "actor", "src", "")
		if rerr != nil {
			t.Fatalf("refresh %d: %v", i, rerr)
		}
		_ = r.Body.Close()
	}

	if got := cache.rows["src"].PreviewFileID; got != first {
		t.Errorf("preview_file_id = %q after refreshes, want the stable %q", got, first)
	}
	if got := cache.rows["src"].SourceUpdatedDate; !got.Equal(t0.Add(3 * time.Minute)) {
		t.Errorf("source_updated_date = %v, want the last rendered date", got)
	}
	if got := files.writeCount(first); got != 3 {
		t.Errorf("in-place replacements = %d, want 3", got)
	}
	if got := countPreviewFiles(files, "src"); got != 1 {
		t.Errorf("logical preview files = %d, want exactly 1 across 4 renders", got)
	}
	if got := atomic.LoadInt32(&renderer.calls); got != 4 {
		t.Errorf("renders = %d, want 4", got)
	}
}

// Content gone but the logical row still present must be repaired under the
// SAME id — a content 404 alone never selects create.
func TestPreviewService_MissingContentRepairsUnderTheSameID(t *testing.T) {
	files := newPreviewFakeFileService()
	t0 := time.Now().UTC().Truncate(time.Second)
	previewSource(files, t0)
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	_ = res.Body.Close()
	previewID := cache.rows["src"].PreviewFileID

	// Content vanishes; the logical row survives, so metadata still resolves.
	files.mu.Lock()
	delete(files.files, previewID)
	files.docs[previewID] = &model.Document{ID: previewID, StorageBucketID: "bucket"}
	files.mu.Unlock()

	r, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("repair Resolve: %v", err)
	}
	_ = r.Body.Close()

	if got := cache.rows["src"].PreviewFileID; got != previewID {
		t.Errorf("preview_file_id = %q after repair, want the same %q", got, previewID)
	}
	if got := files.writeCount(previewID); got != 1 {
		t.Errorf("repair must PUT under the existing id, writes = %d", got)
	}
}

// A genuine metadata 404 — the logical row is gone — is the ONLY case that
// creates a replacement file, publishing id and date together.
func TestPreviewService_MetadataGoneCreatesOneReplacement(t *testing.T) {
	files := newPreviewFakeFileService()
	t0 := time.Now().UTC().Truncate(time.Second)
	previewSource(files, t0)
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	_ = res.Body.Close()
	gone := cache.rows["src"].PreviewFileID

	files.mu.Lock()
	delete(files.files, gone)
	delete(files.docs, gone)
	files.mu.Unlock()

	r, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("replacement Resolve: %v", err)
	}
	_ = r.Body.Close()

	got := cache.rows["src"].PreviewFileID
	if got == gone {
		t.Errorf("preview_file_id still %q; a vanished row must be replaced", gone)
	}
	if got == "" {
		t.Error("replacement id not published")
	}
}

// An upstream PUT failure — 405/5xx/409 alike — must fail the image request
// without creating a second preview file and without advancing the date. A 409
// is never a content collision and never licence to create (ADR 0013 addendum).
func TestPreviewService_WriteFailureNeitherCreatesNorAdvances(t *testing.T) {
	files := newPreviewFakeFileService()
	t0 := time.Now().UTC().Truncate(time.Second)
	previewSource(files, t0)
	cache := newPreviewFakeCache()
	svc := NewPreviewService(files, newAuthorizedActor(), cache, &fakeRenderer{}, 8, zap.NewNop())

	res, err := svc.Resolve(context.Background(), "actor", "src", "")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	_ = res.Body.Close()
	previewID := cache.rows["src"].PreviewFileID

	files.mu.Lock()
	files.writeErr[previewID] = fmt.Errorf("file-service write status 409")
	files.mu.Unlock()
	files.setUpdatedAt("src", t0.Add(time.Minute))

	if _, err := svc.Resolve(context.Background(), "actor", "src", ""); !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("error = %v, want ErrRenderFailed", err)
	}
	if got := cache.rows["src"].PreviewFileID; got != previewID {
		t.Errorf("preview_file_id changed to %q on a failed PUT", got)
	}
	if got := cache.rows["src"].SourceUpdatedDate; !got.Equal(t0) {
		t.Errorf("source_updated_date advanced to %v despite the failed write", got)
	}
	if got := countPreviewFiles(files, "src"); got != 1 {
		t.Errorf("preview files = %d; a failed PUT must not fall back to create", got)
	}
}
