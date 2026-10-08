package collabora

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// ErrInvalidThumbnail is returned when Collabora's thumbnail endpoint
// responds with anything other than a successful image/png body.
var ErrInvalidThumbnail = errors.New("invalid collabora thumbnail response")

// ThumbnailClient renders document previews via Collabora's in-cluster
// POST /cool/get-thumbnail endpoint (port.ThumbnailRenderer).
type ThumbnailClient struct {
	collaboraURL string
	httpClient   *http.Client
}

// NewThumbnailClient creates a ThumbnailClient with no fixed per-request
// timeout — the caller's context is the only timeout source.
func NewThumbnailClient(collaboraURL string) *ThumbnailClient {
	return &ThumbnailClient{collaboraURL: strings.TrimSuffix(collaboraURL, "/"), httpClient: &http.Client{}}
}

// Render streams content into Collabora's "data" multipart part, returning
// the PNG stream the caller must close; neither leg is buffered.
func (c *ThumbnailClient) Render(ctx context.Context, extension string, content io.Reader) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("data", "source."+extension)
		if err == nil {
			_, err = io.Copy(part, content)
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.collaboraURL+"/cool/get-thumbnail", pr)
	if err != nil {
		return nil, fmt.Errorf("create thumbnail request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("thumbnail request: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/png") {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%w: status %d body %q", ErrInvalidThumbnail, resp.StatusCode, body)
	}
	return resp.Body, nil
}
