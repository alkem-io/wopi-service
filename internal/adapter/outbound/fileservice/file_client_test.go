package fileservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// startH2CServer starts an h2c-capable test server on a random port.
func startH2CServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: handler, Protocols: protocols, ReadHeaderTimeout: 5 * time.Second} //nolint:mnd // test timeout

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	return fmt.Sprintf("http://%s", ln.Addr().String())
}

func TestFileClient_FindByID_Success(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/file/doc-1/meta" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metaResponse{
			ID: "doc-1", ExternalID: "ext-1", DisplayName: "test.pdf",
			MimeType: "application/pdf", Size: 999, AuthorizationID: "auth-1",
		})
	}))

	client := NewFileClient(url)
	doc, err := client.FindByID(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document")
	}
	if doc.DisplayName != "test.pdf" {
		t.Errorf("DisplayName = %q", doc.DisplayName)
	}
	if doc.AuthorizationPolicyID != "auth-1" {
		t.Errorf("AuthorizationPolicyID = %q", doc.AuthorizationPolicyID)
	}
}

// TestFileClient_FindByID_PopulatesCreatedByAndUpdatedAt covers the
// fields we added so the WOPI CheckFileInfo response can set stable
// OwnerId and accurate LastModifiedTime.
func TestFileClient_FindByID_PopulatesCreatedByAndUpdatedAt(t *testing.T) {
	creator := "actor-uuid-abc"
	updated := time.Date(2026, 5, 25, 13, 45, 30, 123_000_000, time.UTC)
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metaResponse{
			ID: "doc-1", ExternalID: "ext-1", DisplayName: "report.docx",
			MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Size:     2048, AuthorizationID: "auth-1",
			CreatedBy:   &creator,
			UpdatedDate: updated,
		})
	}))

	client := NewFileClient(url)
	doc, err := client.FindByID(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc.CreatedBy != creator {
		t.Errorf("CreatedBy = %q, want %q", doc.CreatedBy, creator)
	}
	if !doc.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", doc.UpdatedAt, updated)
	}
}

// TestFileClient_FindByID_HandlesMissingCreatedBy covers documents
// returned by file-service without a creator (legacy / system docs).
// CreatedBy is sent as omitempty and decodes to nil; we surface it as
// empty string so CheckFileInfo can detect the fallback path.
func TestFileClient_FindByID_HandlesMissingCreatedBy(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Marshal a payload that omits createdBy entirely
		_, _ = w.Write([]byte(`{"id":"doc-1","externalID":"ext-1","mimeType":"application/pdf","size":1,"displayName":"x.pdf","authorizationId":"auth-1","updatedDate":"2026-01-01T00:00:00Z"}`))
	}))

	client := NewFileClient(url)
	doc, err := client.FindByID(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc.CreatedBy != "" {
		t.Errorf("CreatedBy = %q, want empty when omitted", doc.CreatedBy)
	}
}

func TestFileClient_FindByID_NotFound(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	client := NewFileClient(url)
	doc, err := client.FindByID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil for not found")
	}
}

func TestFileClient_ReadFile_Success(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/file/doc-1/content" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("binary content"))
	}))

	client := NewFileClient(url)
	reader, err := client.ReadFile(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(data) != "binary content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestFileClient_ReadFile_NotFound(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	client := NewFileClient(url)
	_, err := client.ReadFile(context.Background(), "missing")
	if err == nil {
		t.Error("expected error for not found")
	}
}

func TestFileClient_WriteFile_Success(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q", r.Method)
		}
		if r.URL.Path != "/internal/file/doc-1/content" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(port.FileWriteResult{
			ExternalID: "new-hash", Size: 42,
		})
	}))

	client := NewFileClient(url)
	result, err := client.WriteFile(context.Background(), "doc-1", strings.NewReader("new data"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.ExternalID != "new-hash" {
		t.Errorf("ExternalID = %q", result.ExternalID)
	}
	if result.Size != 42 {
		t.Errorf("Size = %d", result.Size)
	}
}

func TestFileClient_WriteFile_NotFound(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	client := NewFileClient(url)
	_, err := client.WriteFile(context.Background(), "missing", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error for not found")
	}
}

func TestFileClient_FileExists_True(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	client := NewFileClient(url)
	exists, err := client.FileExists(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestFileClient_FileExists_False(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	client := NewFileClient(url)
	exists, err := client.FileExists(context.Background(), "missing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if exists {
		t.Error("expected exists=false")
	}
}

func TestFileClient_FindByID_PopulatesStorageBucketID(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metaResponse{ID: "doc-1", StorageBucketID: "bucket-1"})
	}))

	client := NewFileClient(url)
	doc, err := client.FindByID(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc.StorageBucketID != "bucket-1" {
		t.Errorf("StorageBucketID = %q, want %q", doc.StorageBucketID, "bucket-1")
	}
}

// TestFileClient_CreatePreviewFile_SendsSkipDedupAndNoAuthorization proves
// the preview create call omits authorizationId and always sets
// skipDedup=true while streaming the body — the handler reads and
// echoes back the multipart fields it received rather than an assertion on
// a pre-built request, so a streaming regression (e.g. accidental
// buffering that drops a field) would show up as a wrong echoed value.
// requestMultipartFields parses r's multipart body and returns each
// non-file field's first value plus whether it was present at all, and the
// "file" part's bytes.
func requestMultipartFields(t *testing.T, r *http.Request) (fields map[string]string, hasAuth bool, fileBytes []byte) {
	t.Helper()
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // G120: test-only handler, fixed small fixture payload
		t.Fatalf("parse multipart form: %v", err)
	}
	fields = make(map[string]string, len(r.MultipartForm.Value))
	for name, values := range r.MultipartForm.Value {
		if len(values) > 0 {
			fields[name] = values[0]
		}
	}
	_, hasAuth = r.MultipartForm.Value["authorizationId"]
	parts := r.MultipartForm.File["file"]
	if len(parts) == 1 {
		f, err := parts[0].Open()
		if err != nil {
			t.Fatalf("open file part: %v", err)
		}
		defer func() { _ = f.Close() }()
		fileBytes, _ = io.ReadAll(f)
	}
	return fields, hasAuth, fileBytes
}

func TestFileClient_CreatePreviewFile_SendsSkipDedupAndNoAuthorization(t *testing.T) {
	var fields map[string]string
	var hadAuthField bool
	var gotBytes []byte

	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/file" || r.Method != http.MethodPost {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		fields, hadAuthField, gotBytes = requestMultipartFields(t, r)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			ID string `json:"id"`
		}{ID: "preview-1"})
	}))

	client := NewFileClient(url)
	id, err := client.CreatePreviewFile(context.Background(), "bucket-42", strings.NewReader("PNGBYTES"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if id != "preview-1" {
		t.Errorf("id = %q", id)
	}
	if fields["storageBucketId"] != "bucket-42" {
		t.Errorf("storageBucketId = %q", fields["storageBucketId"])
	}
	if fields["skipDedup"] != "true" {
		t.Errorf("skipDedup = %q, want %q", fields["skipDedup"], "true")
	}
	if hadAuthField {
		t.Error("authorizationId must be omitted entirely")
	}
	if string(gotBytes) != "PNGBYTES" {
		t.Errorf("streamed content = %q", gotBytes)
	}
}

func TestFileClient_CreatePreviewFile_NonCreatedStatusIsError(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	client := NewFileClient(url)
	if _, err := client.CreatePreviewFile(context.Background(), "bucket", strings.NewReader("x")); err == nil {
		t.Error("expected error for non-201 status")
	}
}

func TestFileClient_DeletePreviewFile_NotFoundIsNotAnError(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	client := NewFileClient(url)
	if err := client.DeletePreviewFile(context.Background(), "already-gone"); err != nil {
		t.Errorf("expected nil error for already-deleted file, got %v", err)
	}
}

func TestFileClient_DeletePreviewFile_ServerErrorIsError(t *testing.T) {
	url := startH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	client := NewFileClient(url)
	if err := client.DeletePreviewFile(context.Background(), "x"); err == nil {
		t.Error("expected error for server failure")
	}
}
