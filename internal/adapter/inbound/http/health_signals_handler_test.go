package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
	"github.com/alkem-io/wopi-service/internal/domain/service"
	"github.com/alkem-io/wopi-service/internal/obs"
)

// --- failure-injecting port mocks for handler error paths ---

type errFileSvc struct {
	doc      *model.Document
	findErr  error
	writeErr error
}

func (m *errFileSvc) FindByID(_ context.Context, _ string) (*model.Document, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return m.doc, nil
}
func (m *errFileSvc) ReadFile(_ context.Context, _ string) (io.ReadCloser, error) { return nil, nil }
func (m *errFileSvc) WriteFile(_ context.Context, _ string, content io.Reader) (*port.FileWriteResult, error) {
	if m.writeErr != nil {
		return nil, m.writeErr
	}
	_, _ = io.ReadAll(content)
	return &port.FileWriteResult{ExternalID: "ext"}, nil
}
func (m *errFileSvc) FileExists(_ context.Context, _ string) (bool, error) { return false, nil }

type errLockRepo struct{ findErr error }

func (m *errLockRepo) Create(_ context.Context, _ *model.Lock) error { return nil }
func (m *errLockRepo) FindByFileID(_ context.Context, _ string) (*model.Lock, error) {
	return nil, m.findErr
}
func (m *errLockRepo) UpdateLockID(_ context.Context, _, _, _ string, _ model.Lock) error { return nil }
func (m *errLockRepo) RefreshExpiry(_ context.Context, _, _ string, _ *model.Lock) error  { return nil }
func (m *errLockRepo) DeleteByFileID(_ context.Context, _, _ string) error                { return nil }
func (m *errLockRepo) DeleteExpired(_ context.Context) (int64, error)                     { return 0, nil }
func (m *errLockRepo) Takeover(_ context.Context, _, _, _ string, _, _ time.Time) error   { return nil }

type errTokenRepo struct{ createErr error }

func (m *errTokenRepo) Create(_ context.Context, _ *model.AccessToken) error { return m.createErr }
func (m *errTokenRepo) FindByToken(_ context.Context, _ string) (*model.AccessToken, error) {
	return nil, nil
}
func (m *errTokenRepo) DeleteByID(_ context.Context, _ string) error   { return nil }
func (m *errTokenRepo) DeleteExpired(_ context.Context) (int64, error) { return 0, nil }

func obsLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return zap.New(core), logs
}

func signalRecords(logs *observer.ObservedLogs, event string) []observer.LoggedEntry {
	return logs.FilterField(zap.String(obs.FieldEvent, event)).All()
}

const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// ================= T004: PutFile save-failure signal (US1) =================

func putFileReq(docID string, perms string) *http.Request {
	token := &model.AccessToken{FileID: docID, Permissions: perms, ExpiresAt: time.Now().Add(time.Hour)}
	req := reqWithToken(http.MethodPost, "/wopi/files/"+docID+"/contents", strings.NewReader("data"), token)
	req.Header.Set("X-WOPI-Override", "PUT")
	return req
}

func TestPutFile_WriteFailed_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	docID := uuid.New().String()
	wopiSvc := service.NewWOPIService(
		&errFileSvc{doc: &model.Document{ID: docID}, writeErr: errors.New("file-service 500")},
		newHandlerMockLockRepo(), "https://wopi.example.com", "", 0, logger,
	)
	handler := NewWOPIHandler(wopiSvc, nil, logger)

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, putFileReq(docID, "read,write"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	recs := signalRecords(logs, obs.EventPutFile)
	if len(recs) != 1 {
		t.Fatalf("expected 1 putfile record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeWriteFailed)
	assertField(t, recs[0], obs.FieldDocumentID, docID)
	if recs[0].Level != zap.ErrorLevel {
		t.Errorf("level = %v, want error", recs[0].Level)
	}
}

func TestPutFile_LockRepoError_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	docID := uuid.New().String()
	wopiSvc := service.NewWOPIService(
		&errFileSvc{doc: &model.Document{ID: docID}},
		&errLockRepo{findErr: errors.New("lock db down")},
		"https://wopi.example.com", "", 0, logger,
	)
	handler := NewWOPIHandler(wopiSvc, nil, logger)

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, putFileReq(docID, "read,write"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	recs := signalRecords(logs, obs.EventPutFile)
	if len(recs) != 1 {
		t.Fatalf("expected 1 putfile record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeLockRepoError)
}

func TestPutFile_LockConflict_NoRecord(t *testing.T) {
	logger, logs := obsLogger()
	handler, fileSvc, lockRepo := setupWOPIHandlerWith(logger)
	docID := uuid.New().String()
	fileSvc.docs[docID] = &model.Document{ID: docID}
	lockRepo.locks[docID] = &model.Lock{FileID: docID, LockID: "lock-A", ExpiresAt: time.Now().Add(30 * time.Minute)}

	req := putFileReq(docID, "read,write")
	req.Header.Set("X-WOPI-Lock", "wrong-lock")
	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if n := len(signalRecords(logs, obs.EventPutFile)); n != 0 {
		t.Errorf("lock conflict must emit no putfile record, got %d", n)
	}
}

func TestPutFile_NotAuthorized_NoRecord(t *testing.T) {
	logger, logs := obsLogger()
	handler, _, _ := setupWOPIHandlerWith(logger)

	rr := httptest.NewRecorder()
	handler.PutFileContents(rr, putFileReq(uuid.New().String(), "read"))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if n := len(signalRecords(logs, obs.EventPutFile)); n != 0 {
		t.Errorf("auth denial must emit no putfile record, got %d", n)
	}
}

func setupWOPIHandlerWith(logger *zap.Logger) (*WOPIHandler, *handlerMockFileService, *handlerMockLockRepo) {
	fileSvc := newHandlerMockFileService()
	lockRepo := newHandlerMockLockRepo()
	wopiSvc := service.NewWOPIService(fileSvc, lockRepo, "https://wopi.example.com", "", 4*time.Hour, logger)
	return NewWOPIHandler(wopiSvc, nil, logger), fileSvc, lockRepo
}

// ================= T009: token-issuance signal + status pins (US2) =================

func tokenReq(docID string) *http.Request {
	body, _ := json.Marshal(map[string]string{"documentId": docID})
	req := httptest.NewRequest(http.MethodPost, "/wopi/token", bytes.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), actorIDKey, "actor-1"))
}

func TestToken_MetadataLookupFailed_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	tokenSvc := service.NewTokenService(
		&memTokenRepo{tokens: map[string]*model.AccessToken{}},
		&errFileSvc{findErr: errors.New("alkemio db down")},
		&stubAuthSvc{}, testHandlerDiscoverySvc(),
		"secret", "https://wopi.example.com", "https://wopi.example.com", logger,
	)
	rr := httptest.NewRecorder()
	NewTokenHandler(tokenSvc, logger).ServeHTTP(rr, tokenReq(uuid.New().String()))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	recs := signalRecords(logs, obs.EventTokenIssuance)
	if len(recs) != 1 {
		t.Fatalf("expected 1 token record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeMetadataLookup)
	assertField(t, recs[0], obs.FieldActorID, "actor-1")
}

func TestToken_DiscoveryUnavailable503_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	docID := uuid.New().String()
	tokenSvc := service.NewTokenService(
		&memTokenRepo{tokens: map[string]*model.AccessToken{}},
		&errFileSvc{doc: &model.Document{ID: docID, AuthorizationPolicyID: uuid.New().String(), MimeType: docxMIME}},
		&stubAuthSvc{}, nil, // nil discovery svc -> ErrNoDiscoveryData (503)
		"secret", "https://wopi.example.com", "https://wopi.example.com", logger,
	)
	rr := httptest.NewRecorder()
	NewTokenHandler(tokenSvc, logger).ServeHTTP(rr, tokenReq(docID))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (FR-013 pin)", rr.Code)
	}
	recs := signalRecords(logs, obs.EventTokenIssuance)
	if len(recs) != 1 {
		t.Fatalf("expected 1 token record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeDiscoveryUnavail)
}

func TestToken_DiscoveryColdFetch500_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	docID := uuid.New().String()
	// discovery svc whose client errors and was never primed -> cold fetch -> ErrDiscoveryFetch
	coldDisc := service.NewDiscoveryService(&mockDiscoveryClientForHandler{err: io.ErrUnexpectedEOF}, zap.NewNop())
	tokenSvc := service.NewTokenService(
		&memTokenRepo{tokens: map[string]*model.AccessToken{}},
		&errFileSvc{doc: &model.Document{ID: docID, AuthorizationPolicyID: uuid.New().String(), MimeType: docxMIME}},
		&stubAuthSvc{}, coldDisc,
		"secret", "https://wopi.example.com", "https://wopi.example.com", logger,
	)
	rr := httptest.NewRecorder()
	NewTokenHandler(tokenSvc, logger).ServeHTTP(rr, tokenReq(docID))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (FR-013 pin: cold outage stays 500)", rr.Code)
	}
	recs := signalRecords(logs, obs.EventTokenIssuance)
	if len(recs) != 1 {
		t.Fatalf("expected 1 token record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeDiscoveryUnavail)
}

func TestToken_TokenPersistFailed_EmitsRecord(t *testing.T) {
	logger, logs := obsLogger()
	docID := uuid.New().String()
	tokenSvc := service.NewTokenService(
		&errTokenRepo{createErr: errors.New("own db down")},
		&errFileSvc{doc: &model.Document{ID: docID, AuthorizationPolicyID: uuid.New().String(), MimeType: docxMIME}},
		&stubAuthSvc{}, testHandlerDiscoverySvc(),
		"secret", "https://wopi.example.com", "https://wopi.example.com", logger,
	)
	rr := httptest.NewRecorder()
	NewTokenHandler(tokenSvc, logger).ServeHTTP(rr, tokenReq(docID))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	recs := signalRecords(logs, obs.EventTokenIssuance)
	if len(recs) != 1 {
		t.Fatalf("expected 1 token record, got %d", len(recs))
	}
	assertField(t, recs[0], obs.FieldOutcome, outcomeTokenPersist)
}

func TestToken_ClientRejections_NoRecord(t *testing.T) {
	logger, logs := obsLogger()
	// document not found (nil doc) -> 404
	tokenSvc := service.NewTokenService(
		&memTokenRepo{tokens: map[string]*model.AccessToken{}},
		&errFileSvc{doc: nil}, &stubAuthSvc{}, nil,
		"secret", "https://wopi.example.com", "https://wopi.example.com", logger,
	)
	rr := httptest.NewRecorder()
	NewTokenHandler(tokenSvc, logger).ServeHTTP(rr, tokenReq(uuid.New().String()))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if n := len(signalRecords(logs, obs.EventTokenIssuance)); n != 0 {
		t.Errorf("client rejection must emit no token record, got %d", n)
	}
}

// ================= T015: /health Collabora reachability (US3) =================

type fakePinger struct{ err error }

func (p fakePinger) Ping(_ context.Context) error { return p.err }

type fakeProber struct {
	reachable bool
	last      time.Time
	block     bool
}

func (p fakeProber) Probe(ctx context.Context) (bool, time.Time) {
	if p.block {
		<-ctx.Done() // simulate a hung Collabora; unblocks when the probe deadline fires
		return false, time.Time{}
	}
	return p.reachable, p.last
}

func decodeHealth(t *testing.T, rr *httptest.ResponseRecorder) healthResponse {
	t.Helper()
	var resp healthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid health body %q: %v", rr.Body.String(), err)
	}
	return resp
}

func TestHealth_CollaboraReachable_200(t *testing.T) {
	last := time.Now().Add(-time.Minute)
	h := NewHealthHandler(fakePinger{}, nil, fakeProber{reachable: true, last: last}, zap.NewNop())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	resp := decodeHealth(t, rr)
	if resp.Status != "ok" || resp.Collabora != "reachable" {
		t.Errorf("got %+v, want status=ok collabora=reachable", resp)
	}
	if resp.CollaboraLastSuccess == "" {
		t.Error("expected collabora_last_success to be present")
	}
}

func TestHealth_CollaboraUnreachable_Still200(t *testing.T) {
	h := NewHealthHandler(fakePinger{}, nil, fakeProber{reachable: false}, zap.NewNop())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Collabora is soft dep, stays in rotation)", rr.Code)
	}
	resp := decodeHealth(t, rr)
	if resp.Collabora != "unreachable" {
		t.Errorf("collabora = %q, want unreachable", resp.Collabora)
	}
	if resp.CollaboraLastSuccess != "" {
		t.Errorf("last_success must be omitted when never reached, got %q", resp.CollaboraLastSuccess)
	}
}

func TestHealth_HardDepDown_503_NoProbe(t *testing.T) {
	h := NewHealthHandler(fakePinger{err: errors.New("db down")}, nil, fakeProber{reachable: true, last: time.Now()}, zap.NewNop())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	resp := decodeHealth(t, rr)
	if resp.Status != "db_unavailable" {
		t.Errorf("status = %q, want db_unavailable", resp.Status)
	}
	if resp.Collabora != "" {
		t.Errorf("collabora must be absent on the 503 path, got %q", resp.Collabora)
	}
}

// C1 (FR-014): a hung Collabora must not stall /health beyond the probe bound.
func TestHealth_HungCollabora_BoundedReturns200(t *testing.T) {
	h := NewHealthHandler(fakePinger{}, nil, fakeProber{block: true}, zap.NewNop())
	rr := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when Collabora hangs", rr.Code)
	}
	if decodeHealth(t, rr).Collabora != "unreachable" {
		t.Error("hung Collabora must report unreachable")
	}
	if elapsed > collaboraProbeTimeout+2*time.Second {
		t.Errorf("/health took %v; must stay near the %v probe bound", elapsed, collaboraProbeTimeout)
	}
}

// ================= outcome classifier unit coverage =================

func TestPutFileOutcome(t *testing.T) {
	cases := map[error]string{
		service.ErrFileWrite: outcomeWriteFailed,
		service.ErrLockRepo:  outcomeLockRepoError,
		errors.New("other"):  outcomeInternal,
	}
	for err, want := range cases {
		if got := putFileOutcome(err); got != want {
			t.Errorf("putFileOutcome(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestTokenIssuanceOutcome(t *testing.T) {
	cases := map[error]string{
		service.ErrNoDiscoveryData: outcomeDiscoveryUnavail,
		service.ErrDiscoveryFetch:  outcomeDiscoveryUnavail,
		service.ErrDocumentLookup:  outcomeMetadataLookup,
		service.ErrTokenPersist:    outcomeTokenPersist,
		errors.New("other"):        outcomeInternal,
	}
	for err, want := range cases {
		if got := tokenIssuanceOutcome(err); got != want {
			t.Errorf("tokenIssuanceOutcome(%v) = %q, want %q", err, got, want)
		}
	}
}

func assertField(t *testing.T, entry observer.LoggedEntry, key, want string) {
	t.Helper()
	for _, f := range entry.Context {
		if f.Key == key {
			if f.String != want {
				t.Errorf("field %q = %q, want %q", key, f.String, want)
			}
			return
		}
	}
	t.Errorf("field %q not present in record", key)
}
