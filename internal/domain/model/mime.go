package model

import "fmt"

// mimeToExtension maps MIME types to file extensions for editor resolution.
var mimeToExtension = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/msword":                                                        "doc",
	"application/vnd.oasis.opendocument.text":                                   "odt",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.ms-excel":                                                  "xls",
	"application/vnd.oasis.opendocument.spreadsheet":                            "ods",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.ms-powerpoint":                                             "ppt",
	"application/vnd.oasis.opendocument.presentation":                           "odp",
	"application/pdf":                                                           "pdf",
	"text/plain":                                                                "txt",
	"text/csv":                                                                  "csv",
}

// ErrUnsupportedMIME is returned when a MIME type has no extension mapping.
var ErrUnsupportedMIME = fmt.Errorf("unsupported MIME type for editing")

// ExtensionForMIME returns the file extension for a given MIME type.
func ExtensionForMIME(mimeType string) (string, error) {
	ext, ok := mimeToExtension[mimeType]
	if !ok {
		return "", ErrUnsupportedMIME
	}
	return ext, nil
}
