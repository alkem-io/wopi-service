package model

import (
	"errors"
	"testing"
)

func TestPreviewExtension_AllDocumentTypes(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		// word-processing
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"},
		{"application/msword", "doc"},
		{"application/vnd.oasis.opendocument.text", "odt"},
		{"application/rtf", "rtf"},
		// spreadsheet
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
		{"application/vnd.ms-excel", "xls"},
		{"application/vnd.oasis.opendocument.spreadsheet", "ods"},
		{"text/csv", "csv"},
		// presentation
		{"application/vnd.openxmlformats-officedocument.presentationml.presentation", "pptx"},
		{"application/vnd.ms-powerpoint", "ppt"},
		{"application/vnd.oasis.opendocument.presentation", "odp"},
		// drawing
		{"application/vnd.oasis.opendocument.graphics", "odg"},
		// pdf — view-only in the editor, but rendering never writes
		{"application/pdf", "pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			got, err := PreviewExtension(tc.mime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("PreviewExtension(%q) = %q, want %q", tc.mime, got, tc.want)
			}
		})
	}
}

// TestPreviewExtension_ExcludesNonCollaboraTypes proves that a file which is
// merely readable is still not previewable: the allow-list covers the Collabora
// document types and nothing else, so an arbitrary upload can never reach the
// renderer. PDF is deliberately NOT in this list any more — see the supported
// table above and the note on previewExtensionByMIME.
func TestPreviewExtension_ExcludesNonCollaboraTypes(t *testing.T) {
	for _, mimeType := range []string{"text/plain", "image/png", "application/zip", ""} {
		if _, err := PreviewExtension(mimeType); !errors.Is(err, ErrUnsupportedPreviewSource) {
			t.Errorf("PreviewExtension(%q) error = %v, want ErrUnsupportedPreviewSource", mimeType, err)
		}
	}
}
