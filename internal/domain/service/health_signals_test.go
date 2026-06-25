package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/obs"
)

// --- Failure-injecting mocks (error paths the shared happy-path mocks can't hit) ---

type failFileService struct {
	docs      map[string]*model.Document
	findErr   error
	writeErr  error
	writeOKID string
}

func (m *failFileService) FindByID(_ context.Context, id string) (*model.Document, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return m.docs[id], nil
}
func (m *failFileService) ReadFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (m *failFileService) WriteFile(_ context.Context, _ string, content io.Reader) (*port.FileWriteResult, error) {
	if m.writeErr != nil {
		return nil, m.writeErr
	}
	_, _ = io.ReadAll(content)
	return &port.FileWriteResult{ExternalID: m.writeOKID}, nil
}
func (m *failFileService) FileExists(_ context.Context, _ string) (bool, error) { return false, nil }

type failLockRepo struct {
	findErr error
}

func (m *failLockRepo) Create(_ context.Context, _ *model.Lock) error { return nil }
func (m *failLockRepo) FindByFileID(_ context.Context, _ string) (*model.Lock, error) {
	return nil, m.findErr
}
func (m *failLockRepo) UpdateLockID(_ context.Context, _, _, _ string, _ model.Lock) error {
	return nil
}
func (m *failLockRepo) RefreshExpiry(_ context.Context, _, _ string, _ *model.Lock) error {
	return nil
}
func (m *failLockRepo) DeleteByFileID(_ context.Context, _, _ string) error { return nil }
func (m *failLockRepo) DeleteExpired(_ context.Context) (int64, error)      { return 0, nil }
func (m *failLockRepo) Takeover(_ context.Context, _, _, _ string, _, _ time.Time) error {
	return nil
}

type failTokenRepo struct {
	createErr error
}

func (m *failTokenRepo) Create(_ context.Context, _ *model.AccessToken) error { return m.createErr }
func (m *failTokenRepo) FindByToken(_ context.Context, _ string) (*model.AccessToken, error) {
	return nil, nil
}
func (m *failTokenRepo) DeleteByID(_ context.Context, _ string) error   { return nil }
func (m *failTokenRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

// --- T003: PutFile sentinel wrapping (FR-003) ---

func TestPutFile_WrapsLockRepoError(t *testing.T) {
	svc := NewWOPIService(
		&failFileService{docs: map[string]*model.Document{"doc-1": {ID: "doc-1"}}},
		&failLockRepo{findErr: errors.New("db down")},
		"https://wopi.example.com", "", 0, zap.NewNop(),
	)
	token := &model.AccessToken{FileID: "doc-1", Permissions: "read,write"}

	_, err := svc.PutFile(context.Background(), token, "lock-1", strings.NewReader("data"))
	if !errors.Is(err, ErrLockRepo) {
		t.Fatalf("expected ErrLockRepo, got %v", err)
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("underlying error not preserved: %v", err)
	}
}

func TestPutFile_WrapsFileWriteError(t *testing.T) {
	svc := NewWOPIService(
		&failFileService{
			docs:     map[string]*model.Document{"doc-1": {ID: "doc-1"}},
			writeErr: errors.New("file-service 500"),
		},
		&failLockRepo{}, // no lock, no error
		"https://wopi.example.com", "", 0, zap.NewNop(),
	)
	token := &model.AccessToken{FileID: "doc-1", Permissions: "read,write"}

	_, err := svc.PutFile(context.Background(), token, "", strings.NewReader("data"))
	if !errors.Is(err, ErrFileWrite) {
		t.Fatalf("expected ErrFileWrite, got %v", err)
	}
	if !strings.Contains(err.Error(), "file-service 500") {
		t.Errorf("underlying error not preserved: %v", err)
	}
}

// --- T007: token-issuance sentinel wrapping (FR-006) ---

func TestIssueToken_WrapsDocumentLookupError(t *testing.T) {
	svc := NewTokenService(
		newMockTokenRepo(),
		&failFileService{findErr: errors.New("alkemio db down")},
		newMockAuthSvc(), testDiscoverySvc(),
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)

	_, err := svc.IssueToken(context.Background(), "actor-1", "Actor", "doc-1")
	if !errors.Is(err, ErrDocumentLookup) {
		t.Fatalf("expected ErrDocumentLookup, got %v", err)
	}
	if !strings.Contains(err.Error(), "alkemio db down") {
		t.Errorf("underlying error not preserved: %v", err)
	}
}

func TestIssueToken_WrapsTokenPersistError(t *testing.T) {
	docID := uuid.New().String()
	actorID := uuid.New().String()
	fileSvc := &failFileService{docs: map[string]*model.Document{docID: {
		ID:                    docID,
		AuthorizationPolicyID: uuid.New().String(),
		MimeType:              "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}}}
	authSvc := newMockAuthSvc()
	authSvc.results[actorID+":read"] = true

	svc := NewTokenService(
		&failTokenRepo{createErr: errors.New("own db down")},
		fileSvc, authSvc, testDiscoverySvc(),
		"secret", "https://wopi.example.com", "https://wopi.example.com", zap.NewNop(),
	)

	_, err := svc.IssueToken(context.Background(), actorID, "Actor", docID)
	if !errors.Is(err, ErrTokenPersist) {
		t.Fatalf("expected ErrTokenPersist, got %v", err)
	}
	if !strings.Contains(err.Error(), "own db down") {
		t.Errorf("underlying error not preserved: %v", err)
	}
}

// --- T008: cold discovery-fetch wraps ErrDiscoveryFetch; stale fallback stays clean ---

func TestGetDiscovery_ColdFetchWrapsErrDiscoveryFetch(t *testing.T) {
	svc := NewDiscoveryService(&mockDiscoveryClient{err: errors.New("collabora down")}, zap.NewNop())

	_, err := svc.GetDiscovery(context.Background())
	if !errors.Is(err, ErrDiscoveryFetch) {
		t.Fatalf("expected ErrDiscoveryFetch on cold fetch, got %v", err)
	}
}

func TestGetDiscovery_StaleFallbackNoError(t *testing.T) {
	client := &mockDiscoveryClient{data: &port.DiscoveryData{Actions: []port.DiscoveryAction{{Ext: "docx"}}}}
	svc := NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background()) // prime

	svc.mu.Lock()
	svc.cachedAt = svc.cachedAt.Add(-svc.cacheTTL * 2)
	svc.mu.Unlock()
	client.err = errors.New("collabora down")
	client.data = nil

	if _, err := svc.GetDiscovery(context.Background()); err != nil {
		t.Fatalf("stale fallback must not error (no failure record): %v", err)
	}
}

// --- T014: Probe transitions, baseline, lastSuccess, concurrency (FR-011/SC-003) ---

func newProbeSvc(client *mockDiscoveryClient) (*DiscoveryService, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return NewDiscoveryService(client, zap.New(core)), logs
}

func reachLogs(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterField(zap.String(obs.FieldEvent, obs.EventCollaboraReachability)).All()
}

func TestProbe_BaselineUpSilent(t *testing.T) {
	svc, logs := newProbeSvc(&mockDiscoveryClient{data: &port.DiscoveryData{}})
	reachable, last := svc.Probe(context.Background())
	if !reachable {
		t.Fatal("expected reachable on success")
	}
	if last.IsZero() {
		t.Error("lastSuccess should be set on a successful probe")
	}
	if n := len(reachLogs(logs)); n != 0 {
		t.Errorf("baseline must not log a transition, got %d records", n)
	}
}

func TestProbe_BaselineDownSilent(t *testing.T) {
	svc, logs := newProbeSvc(&mockDiscoveryClient{err: errors.New("down")})
	reachable, last := svc.Probe(context.Background())
	if reachable {
		t.Fatal("expected unreachable on failure")
	}
	if !last.IsZero() {
		t.Error("lastSuccess must remain zero when never reached")
	}
	if n := len(reachLogs(logs)); n != 0 {
		t.Errorf("down baseline must not log, got %d records", n)
	}
}

func TestProbe_UpToDownLogsWarnOnce(t *testing.T) {
	client := &mockDiscoveryClient{data: &port.DiscoveryData{}}
	svc, logs := newProbeSvc(client)
	svc.Probe(context.Background()) // baseline up

	client.err = errors.New("collabora down")
	client.data = nil
	svc.Probe(context.Background()) // up -> down (warn)
	svc.Probe(context.Background()) // still down (silent)

	entries := reachLogs(logs)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one transition record, got %d", len(entries))
	}
	if entries[0].Level != zap.WarnLevel {
		t.Errorf("lost transition must be warn, got %v", entries[0].Level)
	}
}

func TestProbe_DownToUpLogsInfoOnce(t *testing.T) {
	client := &mockDiscoveryClient{err: errors.New("down")}
	svc, logs := newProbeSvc(client)
	svc.Probe(context.Background()) // baseline down

	client.err = nil
	client.data = &port.DiscoveryData{}
	svc.Probe(context.Background()) // down -> up (info)
	svc.Probe(context.Background()) // still up (silent)

	entries := reachLogs(logs)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one transition record, got %d", len(entries))
	}
	if entries[0].Level != zap.InfoLevel {
		t.Errorf("regained transition must be info, got %v", entries[0].Level)
	}
}

func TestProbe_LastSuccessOnlyOnSuccess(t *testing.T) {
	client := &mockDiscoveryClient{data: &port.DiscoveryData{}}
	svc, _ := newProbeSvc(client)
	_, first := svc.Probe(context.Background())

	client.err = errors.New("down")
	client.data = nil
	_, afterDown := svc.Probe(context.Background())

	if !first.Equal(afterDown) {
		t.Errorf("lastSuccess changed on a failed probe: %v -> %v", first, afterDown)
	}
}

func TestProbe_ConcurrentTransitionLogsOnce(t *testing.T) {
	client := &mockDiscoveryClient{data: &port.DiscoveryData{}}
	svc, logs := newProbeSvc(client)
	svc.Probe(context.Background()) // baseline up

	client.err = errors.New("down")
	client.data = nil

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Probe(context.Background())
		}()
	}
	wg.Wait()

	if n := len(reachLogs(logs)); n != 1 {
		t.Errorf("concurrent up->down probes must log exactly one warn, got %d", n)
	}
}
