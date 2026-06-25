package collabora

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testDiscoveryXML = `<?xml version="1.0" encoding="utf-8"?>
<wopi-discovery>
  <net-zone name="external-https">
    <app name="Word" favIconUrl="https://collabora/favicon.ico">
      <action name="edit" ext="docx" urlsrc="https://collabora/edit?WOPISrc="/>
      <action name="view" ext="docx" urlsrc="https://collabora/view?WOPISrc="/>
    </app>
    <app name="Calc">
      <action name="edit" ext="xlsx" urlsrc="https://collabora/edit?WOPISrc="/>
    </app>
  </net-zone>
  <proof-key modulus="abc" exponent="def" oldmodulus="old-abc" oldexponent="old-def"/>
</wopi-discovery>`

func TestDiscoveryClient_FetchDiscovery_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hosting/discovery" {
			t.Errorf("path = %q, want /hosting/discovery", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(testDiscoveryXML))
	}))
	defer srv.Close()

	client := NewDiscoveryClient(srv.URL)
	data, err := client.FetchDiscovery(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(data.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(data.Actions))
	}

	// Check first action
	if data.Actions[0].App != "Word" || data.Actions[0].Ext != "docx" || data.Actions[0].Name != "edit" {
		t.Errorf("action[0] = %+v", data.Actions[0])
	}

	// Check proof keys
	if data.ProofKey.Modulus != "abc" {
		t.Errorf("Modulus = %q", data.ProofKey.Modulus)
	}
	if data.ProofKey.OldModulus != "old-abc" {
		t.Errorf("OldModulus = %q", data.ProofKey.OldModulus)
	}
}

func TestDiscoveryClient_FetchDiscovery_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewDiscoveryClient(srv.URL)
	_, err := client.FetchDiscovery(context.Background())
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestDiscoveryClient_FetchDiscovery_InvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not xml"))
	}))
	defer srv.Close()

	client := NewDiscoveryClient(srv.URL)
	_, err := client.FetchDiscovery(context.Background())
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

func TestDiscoveryClient_FetchDiscovery_Unreachable(t *testing.T) {
	client := NewDiscoveryClient("http://127.0.0.1:1") // nothing listening
	_, err := client.FetchDiscovery(context.Background())
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// A reverse proxy can return 200 with a placeholder page (well-formed XML/HTML
// but NOT a <wopi-discovery> root) while coolwsd is still warming up. That must
// count as unreachable — a 2xx alone is not "serving WOPI" (clarification).
func TestDiscoveryClient_FetchDiscovery_PlaceholderBody_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body>default site</body></html>`))
	}))
	defer srv.Close()

	client := NewDiscoveryClient(srv.URL)
	if _, err := client.FetchDiscovery(context.Background()); err == nil {
		t.Error("expected error: a 200 with a non-wopi-discovery body must be treated as unreachable")
	}
}

// The discovery body read is bounded (LimitReader) so a hung/oversized response
// cannot blow the probe budget. A body larger than the cap is truncated and
// fails to parse — i.e. counts as unreachable rather than being read unbounded.
func TestDiscoveryClient_FetchDiscovery_BoundedBody(t *testing.T) {
	oversized := bytes.Repeat([]byte("A"), maxDiscoveryBytes+4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(oversized)
	}))
	defer srv.Close()

	done := make(chan error, 1)
	client := NewDiscoveryClient(srv.URL)
	go func() { _, err := client.FetchDiscovery(context.Background()); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error for oversized non-discovery body")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FetchDiscovery did not return promptly — body read may be unbounded")
	}
}
