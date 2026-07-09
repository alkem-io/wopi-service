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
	// Collabora accepts a host-initiated Action_RenameFile postMessage (writers
	// only) and relabels its title bar live. On rename the handler persists the
	// new name authoritatively via the server and echoes it straight back to
	// Collabora (see wopi_handler.go renameFile). The Alkemio side stays the
	// source of truth, so if that persist fails the name reverts to the stored
	// value on reopen rather than drifting.
	SupportsRename          bool   `json:"SupportsRename"`
	UserCanRename           bool   `json:"UserCanRename"`
	UserCanNotWriteRelative bool   `json:"UserCanNotWriteRelative"`
	LastModifiedTime        string `json:"LastModifiedTime,omitempty"`
	PostMessageOrigin       string `json:"PostMessageOrigin,omitempty"`
	ReadOnly                bool   `json:"ReadOnly,omitempty"`
}
