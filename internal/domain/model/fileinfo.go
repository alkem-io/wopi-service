package model

// FileInfo represents the WOPI CheckFileInfo response.
// Field names use WOPI protocol naming (OwnerId, UserId) which differs
// from Go convention (OwnerID, UserID) — the JSON tags must match the spec.
type FileInfo struct {
	BaseFileName            string `json:"BaseFileName"`
	OwnerID                 string `json:"OwnerId"` //nolint:revive,staticcheck // WOPI protocol field name
	Size                    int64  `json:"Size"`
	UserID                  string `json:"UserId"` //nolint:revive,staticcheck // WOPI protocol field name
	Version                 string `json:"Version"`
	UserFriendlyName        string `json:"UserFriendlyName,omitempty"`
	UserCanWrite            bool   `json:"UserCanWrite"`
	SupportsLocks           bool   `json:"SupportsLocks"`
	SupportsUpdate          bool   `json:"SupportsUpdate"`
	UserCanNotWriteRelative bool   `json:"UserCanNotWriteRelative"`
	LastModifiedTime        string `json:"LastModifiedTime,omitempty"`
	PostMessageOrigin       string `json:"PostMessageOrigin,omitempty"`
	ReadOnly                bool   `json:"ReadOnly,omitempty"`
}
