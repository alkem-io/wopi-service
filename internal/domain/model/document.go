package model

import "time"

// Document represents file metadata sourced from file-service-go.
type Document struct {
	ID                    string
	ExternalID            string
	DisplayName           string
	MimeType              string
	Size                  int64
	AuthorizationPolicyID string

	// CreatedBy is the actor UUID of the document's original creator. May
	// be empty for legacy or system-created documents — callers that need
	// a stable per-file identity (WOPI CheckFileInfo `OwnerId`) should
	// fall back to ID when this is empty so the value remains constant
	// across viewers.
	CreatedBy string

	// UpdatedAt is the last modification time from file-service-go. Used
	// to populate WOPI CheckFileInfo `LastModifiedTime`. Zero value means
	// the upstream did not provide one; emit no LastModifiedTime in that
	// case (the field is optional in the WOPI spec).
	UpdatedAt time.Time
}
