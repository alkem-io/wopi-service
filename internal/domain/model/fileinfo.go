package model

// FileInfo represents the WOPI CheckFileInfo response.
// Field names use WOPI protocol naming (OwnerId, UserId) which differs
// from Go convention (OwnerID, UserID) — the JSON tags must match the spec.
type FileInfo struct {
	BaseFileName     string `json:"BaseFileName"`
	OwnerID          string `json:"OwnerId"` //nolint:revive,staticcheck // WOPI protocol field name
	Size             int64  `json:"Size"`
	UserID           string `json:"UserId"` //nolint:revive,staticcheck // WOPI protocol field name
	Version          string `json:"Version"`
	UserFriendlyName string `json:"UserFriendlyName,omitempty"`
	UserCanWrite     bool   `json:"UserCanWrite"`
	SupportsLocks    bool   `json:"SupportsLocks"`
	SupportsUpdate   bool   `json:"SupportsUpdate"`
	// SupportsRename + UserCanRename advertise the WOPI RenameFile operation so
	// Collabora accepts a host-initiated Action_RenameFile postMessage and
	// relabels its title bar live. The rename itself is authoritative on the
	// Alkemio side; the RenameFile handler just echoes the current name (see
	// wopi_handler.go renameFile), so an in-editor rename reverts to the
	// backend truth rather than drifting.
	SupportsRename          bool   `json:"SupportsRename"`
	UserCanRename           bool   `json:"UserCanRename"`
	UserCanNotWriteRelative bool   `json:"UserCanNotWriteRelative"`
	LastModifiedTime        string `json:"LastModifiedTime,omitempty"`
	PostMessageOrigin       string `json:"PostMessageOrigin,omitempty"`
	ReadOnly                bool   `json:"ReadOnly,omitempty"`
}
