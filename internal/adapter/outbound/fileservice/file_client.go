// Package fileservice implements the file I/O and document metadata adapter
// using file-service-go's cluster-internal HTTP endpoints.
package fileservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// FileClient implements port.FileService and port.DocumentRepository
// via file-service-go's private endpoints.
type FileClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewFileClient creates a new FileClient.
func NewFileClient(baseURL string) *FileClient {
	return &FileClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// metaResponse matches the GET /internal/document/:id/meta response.
type metaResponse struct {
	ID              string `json:"id"`
	ExternalID      string `json:"externalID"`
	MimeType        string `json:"mimeType"`
	Size            int64  `json:"size"`
	DisplayName     string `json:"displayName"`
	CreatedBy       string `json:"createdBy"`
	AuthorizationID string `json:"authorizationId"`
}

// FindByID retrieves document metadata from file-service-go.
func (c *FileClient) FindByID(ctx context.Context, documentID string) (*model.Document, error) {
	url := fmt.Sprintf("%s/internal/document/%s/meta", c.baseURL, documentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create meta request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("file-service meta: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file-service meta status %d", resp.StatusCode)
	}

	var meta metaResponse
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode meta response: %w", err)
	}

	return &model.Document{
		ID:                    meta.ID,
		ExternalID:            meta.ExternalID,
		DisplayName:           meta.DisplayName,
		MimeType:              meta.MimeType,
		Size:                  meta.Size,
		AuthorizationPolicyID: meta.AuthorizationID,
	}, nil
}

// ReadFile returns the content of a file by document ID.
func (c *FileClient) ReadFile(ctx context.Context, documentID string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/internal/document/%s/content", c.baseURL, documentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create read request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("file-service read: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("file not found: %s", documentID)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("file-service read status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// WriteFile replaces file content for a document (store-and-link).
func (c *FileClient) WriteFile(ctx context.Context, documentID string, content io.Reader) (*port.FileWriteResult, error) {
	url := fmt.Sprintf("%s/internal/document/%s/content", c.baseURL, documentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, content)
	if err != nil {
		return nil, fmt.Errorf("create write request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("file-service write: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("document not found: %s", documentID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file-service write status %d", resp.StatusCode)
	}

	var result port.FileWriteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode write response: %w", err)
	}
	return &result, nil
}

// FileExists checks whether a document's file exists in storage.
func (c *FileClient) FileExists(ctx context.Context, documentID string) (bool, error) {
	url := fmt.Sprintf("%s/internal/document/%s/content", c.baseURL, documentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, fmt.Errorf("create exists request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("file-service exists: %w", err)
	}
	_ = resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}
