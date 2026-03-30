package port

import (
	"context"
	"io"
)

// FileWriteResult holds the result of a file write operation.
type FileWriteResult struct {
	ExternalID string `json:"externalID"`
	Size       int64  `json:"size"`
}

// FileService provides file read/write via file-service-go.
type FileService interface {
	// ReadFile returns the content of a file by its externalID.
	ReadFile(ctx context.Context, externalID string) (io.ReadCloser, error)
	// WriteFile writes content and updates the document record atomically.
	WriteFile(ctx context.Context, documentID string, content io.Reader) (*FileWriteResult, error)
	// FileExists checks whether a file exists in storage.
	FileExists(ctx context.Context, externalID string) (bool, error)
}
