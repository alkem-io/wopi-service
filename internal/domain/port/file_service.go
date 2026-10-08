// Package port defines domain interfaces (driven and driving ports).
package port

import (
	"context"
	"io"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// FileWriteResult holds the result of a file write operation.
type FileWriteResult struct {
	ExternalID string `json:"externalID"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
}

// FileService provides file read/write and document metadata via file-service.
type FileService interface {
	// FindByID retrieves document metadata by UUID. Returns nil if not found.
	FindByID(ctx context.Context, documentID string) (*model.Document, error)
	// ReadFile returns the content of a file by document ID.
	ReadFile(ctx context.Context, documentID string) (io.ReadCloser, error)
	// WriteFile replaces file content for a document (store-and-link).
	WriteFile(ctx context.Context, documentID string, content io.Reader) (*FileWriteResult, error)
	// CreatePreviewFile streams content into a NEW private file (no
	// authorizationId, skipDedup=true) in storageBucketID.
	CreatePreviewFile(ctx context.Context, storageBucketID string, content io.Reader) (fileID string, err error)
	// DeletePreviewFile best-effort deletes a superseded preview file; a
	// file that is already gone is not an error.
	DeletePreviewFile(ctx context.Context, fileID string) error
}
