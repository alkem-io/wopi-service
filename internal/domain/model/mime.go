package model

import "fmt"

// MimeTypePDF is application/pdf, called out as a named constant because it
// gets a special permissions rule in TokenService.IssueToken (always
// read-only — see the comment there).
const MimeTypePDF = "application/pdf"

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
	MimeTypePDF:  "pdf",
	"text/plain": "txt",
	"text/csv":   "csv",
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
