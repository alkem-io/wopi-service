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

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// startH2CServer starts an h2c-capable test server on a random port.
func startH2CServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	h2s := &http2.Server{}
	srv := &http.Server{Handler: h2c.NewHandler(handler, h2s), ReadHeaderTimeout: 5 * time.Second} //nolint:mnd // test timeout

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
		if r.URL.Path != "/internal/document/doc-1/meta" {
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
		if r.URL.Path != "/internal/document/doc-1/content" {
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
		if r.URL.Path != "/internal/document/doc-1/content" {
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
