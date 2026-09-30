package collabora

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThumbnailClient_Render_Success(t *testing.T) {
	var gotPath, gotField, gotFilename string
	var gotBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("content-type: %v %v", mediaType, err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		gotField = part.FormName()
		gotFilename = part.FileName()
		gotBytes, _ = io.ReadAll(part)

		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()

	client := NewThumbnailClient(srv.URL)
	png, err := client.Render(context.Background(), "docx", strings.NewReader("SOURCE BYTES"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	defer func() { _ = png.Close() }()

	got, err := io.ReadAll(png)
	if err != nil {
		t.Fatalf("read png: %v", err)
	}
	if string(got) != "PNGDATA" {
		t.Errorf("png body = %q", got)
	}
	if gotPath != "/cool/get-thumbnail" {
		t.Errorf("path = %q", gotPath)
	}
	if gotField != "data" {
		t.Errorf("multipart field = %q, want %q", gotField, "data")
	}
	if !strings.HasSuffix(gotFilename, ".docx") {
		t.Errorf("filename = %q, want *.docx", gotFilename)
	}
	if string(gotBytes) != "SOURCE BYTES" {
		t.Errorf("streamed source = %q", gotBytes)
	}
}

func TestThumbnailClient_Render_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewThumbnailClient(srv.URL)
	_, err := client.Render(context.Background(), "docx", strings.NewReader("x"))
	if !errors.Is(err, ErrInvalidThumbnail) {
		t.Errorf("error = %v, want ErrInvalidThumbnail", err)
	}
}

func TestThumbnailClient_Render_WrongContentTypeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewThumbnailClient(srv.URL)
	_, err := client.Render(context.Background(), "docx", strings.NewReader("x"))
	if !errors.Is(err, ErrInvalidThumbnail) {
		t.Errorf("error = %v, want ErrInvalidThumbnail", err)
	}
}
