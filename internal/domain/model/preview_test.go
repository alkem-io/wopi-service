package model

import (
	"errors"
	"testing"
)

func TestPreviewExtension_AllFourDocumentTypes(t *testing.T) {
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

// TestPreviewExtension_ExcludesOutOfScopeTypes proves PDF and other
// Collabora-editable-but-out-of-scope MIME types are rejected: preview
// covers only the four existing Collabora document types, not
// every type wopi-service can open in the editor.
func TestPreviewExtension_ExcludesOutOfScopeTypes(t *testing.T) {
	for _, mimeType := range []string{"application/pdf", "text/plain", "image/png", ""} {
		if _, err := PreviewExtension(mimeType); !errors.Is(err, ErrUnsupportedPreviewSource) {
			t.Errorf("PreviewExtension(%q) error = %v, want ErrUnsupportedPreviewSource", mimeType, err)
		}
	}
}
