// Package fileservice implements the file I/O and document metadata adapter
// using file-service's cluster-internal HTTP endpoints.
package fileservice

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/alkem-io/wopi-service/internal/domain/model"
	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// FileClient implements port.FileService and port.DocumentRepository
// via file-service's private endpoints using h2c (HTTP/2 cleartext).
type FileClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewFileClient creates a new h2c-capable FileClient.
func NewFileClient(baseURL string) *FileClient {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	return &FileClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

// metaResponse matches the GET /internal/file/:id/meta response.
// CreatedBy is *string because file-service marshals it as
// `omitempty` — absent for documents with no recorded creator.
type metaResponse struct {
	ID              string    `json:"id"`
	ExternalID      string    `json:"externalID"`
	MimeType        string    `json:"mimeType"`
	Size            int64     `json:"size"`
	DisplayName     string    `json:"displayName"`
	CreatedBy       *string   `json:"createdBy,omitempty"`
	AuthorizationID string    `json:"authorizationId"`
	UpdatedDate     time.Time `json:"updatedDate"`
	StorageBucketID string    `json:"storageBucketId"`
}

// FindByID retrieves document metadata from file-service.
func (c *FileClient) FindByID(ctx context.Context, documentID string) (*model.Document, error) {
	url := fmt.Sprintf("%s/internal/file/%s/meta", c.baseURL, documentID)
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

	createdBy := ""
	if meta.CreatedBy != nil {
		createdBy = *meta.CreatedBy
	}

	return &model.Document{
		ID:                    meta.ID,
		ExternalID:            meta.ExternalID,
		DisplayName:           meta.DisplayName,
		MimeType:              meta.MimeType,
		Size:                  meta.Size,
		AuthorizationPolicyID: meta.AuthorizationID,
		CreatedBy:             createdBy,
		UpdatedAt:             meta.UpdatedDate,
		StorageBucketID:       meta.StorageBucketID,
	}, nil
}

// ReadFile returns the content of a file by document ID.
func (c *FileClient) ReadFile(ctx context.Context, documentID string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/internal/file/%s/content", c.baseURL, documentID)
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
	url := fmt.Sprintf("%s/internal/file/%s/content", c.baseURL, documentID)
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
	url := fmt.Sprintf("%s/internal/file/%s/content", c.baseURL, documentID)
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

// CreatePreviewFile streams content into a NEW private file in
// storageBucketID (skipDedup=true, no authorizationId) without buffering it.
func (c *FileClient) CreatePreviewFile(ctx context.Context, storageBucketID string, content io.Reader) (string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := mw.WriteField("storageBucketId", storageBucketID)
		if err == nil {
			err = mw.WriteField("skipDedup", "true")
		}
		if err == nil {
			err = mw.WriteField("displayName", "collabora-preview.png")
		}
		if err == nil {
			var part io.Writer
			part, err = mw.CreateFormFile("file", "collabora-preview.png")
			if err == nil {
				_, err = io.Copy(part, content)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/file", pr)
	if err != nil {
		return "", fmt.Errorf("create preview-file request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("file-service create preview: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("file-service create preview status %d", resp.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("decode create preview response: %w", err)
	}
	return created.ID, nil
}

// DeletePreviewFile best-effort deletes a superseded private preview file.
// A 404 (already gone) is not an error.
func (c *FileClient) DeletePreviewFile(ctx context.Context, fileID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/internal/file/"+fileID, nil)
	if err != nil {
		return fmt.Errorf("create delete-preview request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("file-service delete preview: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("file-service delete preview status %d", resp.StatusCode)
	}
	return nil
}
