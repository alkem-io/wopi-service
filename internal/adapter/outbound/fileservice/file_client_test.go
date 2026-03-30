package fileservice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

func TestFileClient_FindByID_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/document/doc-1/meta" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metaResponse{
			ID: "doc-1", ExternalID: "ext-1", DisplayName: "test.pdf",
			MimeType: "application/pdf", Size: 999, AuthorizationID: "auth-1",
		})
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	doc, err := client.FindByID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil for not found")
	}
}

func TestFileClient_ReadFile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/document/doc-1/content" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("binary content"))
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	reader, err := client.ReadFile(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer func() { _ = reader.Close() }()

	data, _ := io.ReadAll(reader)
	if string(data) != "binary content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestFileClient_ReadFile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	_, err := client.ReadFile(context.Background(), "missing")
	if err == nil {
		t.Error("expected error for not found")
	}
}

func TestFileClient_WriteFile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer srv.Close()

	client := NewFileClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	_, err := client.WriteFile(context.Background(), "missing", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error for not found")
	}
}

func TestFileClient_FileExists_True(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %q", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	exists, err := client.FileExists(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestFileClient_FileExists_False(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewFileClient(srv.URL)
	exists, err := client.FileExists(context.Background(), "missing")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if exists {
		t.Error("expected exists=false")
	}
}
