package model

import (
	"fmt"
	"time"
)

// PreviewCacheEntry is WOPI's row mapping a source file to the private
// preview file currently representing it and the rendered source updatedDate.
type PreviewCacheEntry struct {
	SourceFileID      string
	PreviewFileID     string
	SourceUpdatedDate time.Time
}

// previewExtensionByMIME maps the four existing Collabora document types to
// the extension Collabora's thumbnail broker needs (mirrors server's
// MIME_TO_DOCUMENT_TYPE allow-list, excluding the view-only PDF type).
var previewExtensionByMIME = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/msword":                      "doc",
	"application/vnd.oasis.opendocument.text": "odt",
	"application/rtf":                         "rtf",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	"application/vnd.ms-excel":                       "xls",
	"application/vnd.oasis.opendocument.spreadsheet": "ods",
	"text/csv": "csv",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.ms-powerpoint":                                             "ppt",
	"application/vnd.oasis.opendocument.presentation":                           "odp",
	"application/vnd.oasis.opendocument.graphics":                               "odg",
}

// ErrUnsupportedPreviewSource is returned for a MIME type outside the four
// existing Collabora document types eligible for preview.
var ErrUnsupportedPreviewSource = fmt.Errorf("source is not a previewable Collabora document type")

// PreviewExtension returns the file extension Collabora needs for
// mimeType, or ErrUnsupportedPreviewSource.
func PreviewExtension(mimeType string) (string, error) {
	ext, ok := previewExtensionByMIME[mimeType]
	if !ok {
		return "", ErrUnsupportedPreviewSource
	}
	return ext, nil
}
