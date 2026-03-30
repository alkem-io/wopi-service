// Package fileservice implements the file I/O adapter using file-service-go HTTP endpoints.
package fileservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// FileClient implements port.FileService via file-service-go private endpoints.
type FileClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewFileClient creates a new FileClient.
func NewFileClient(baseURL string) *FileClient {
	return &FileClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

// ReadFile returns the content of a file by its externalID.
func (c *FileClient) ReadFile(ctx context.Context, externalID string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/internal/storage/%s", c.baseURL, externalID)
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
		return nil, fmt.Errorf("file not found: %s", externalID)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("file-service read status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// WriteFile writes content and updates the document record atomically.
func (c *FileClient) WriteFile(ctx context.Context, documentID string, content io.Reader) (*port.FileWriteResult, error) {
	url := fmt.Sprintf("%s/internal/storage/document/%s", c.baseURL, documentID)
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

// FileExists checks whether a file exists in storage.
func (c *FileClient) FileExists(ctx context.Context, externalID string) (bool, error) {
	url := fmt.Sprintf("%s/internal/storage/%s", c.baseURL, externalID)
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
