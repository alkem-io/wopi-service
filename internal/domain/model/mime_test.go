package model

import (
	"errors"
	"testing"
)

func TestExtensionForMIME_AllMapped(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"},
		{"application/msword", "doc"},
		{"application/vnd.oasis.opendocument.text", "odt"},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
		{"application/vnd.ms-excel", "xls"},
		{"application/vnd.oasis.opendocument.spreadsheet", "ods"},
		{"application/vnd.openxmlformats-officedocument.presentationml.presentation", "pptx"},
		{"application/vnd.ms-powerpoint", "ppt"},
		{"application/vnd.oasis.opendocument.presentation", "odp"},
		{"application/pdf", "pdf"},
		{"text/plain", "txt"},
		{"text/csv", "csv"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			got, err := ExtensionForMIME(tc.mime)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ExtensionForMIME(%q) = %q, want %q", tc.mime, got, tc.want)
			}
		})
	}
}

func TestExtensionForMIME_Unsupported(t *testing.T) {
	_, err := ExtensionForMIME("image/png")
	if !errors.Is(err, ErrUnsupportedMIME) {
		t.Errorf("expected ErrUnsupportedMIME, got %v", err)
	}
}

func TestExtensionForMIME_Empty(t *testing.T) {
	_, err := ExtensionForMIME("")
	if !errors.Is(err, ErrUnsupportedMIME) {
		t.Errorf("expected ErrUnsupportedMIME for empty string, got %v", err)
	}
}
