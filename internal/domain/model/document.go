package model

// Document represents file metadata from Alkemio's database (read-only).
type Document struct {
	ID                    string
	ExternalID            string
	DisplayName           string
	MimeType              string
	Size                  int64
	AuthorizationPolicyID string
}
