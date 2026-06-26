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
// cannot blow the probe budget. The server writes exactly the cap's worth of
// bytes and then holds the connection open WITHOUT closing it (no EOF). This is
// what makes the test defend the invariant: a bounded read returns promptly once
// the cap is reached (the all-"A" body fails XML parsing → error), whereas an
// unbounded io.ReadAll would block waiting for EOF until the client timeout —
// caught by the deadline below. (A server that wrote a finite body and closed
// would let an unbounded read pass too, so it would NOT distinguish bounded from
// unbounded.)
func TestDiscoveryClient_FetchDiscovery_BoundedBody(t *testing.T) {
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server ResponseWriter is not an http.Flusher")
			return
		}
		w.WriteHeader(http.StatusOK)
		// Exactly the cap: a bounded reader consumes all of it and stops (no
		// extra bytes left for the server to block on); an unbounded reader
		// consumes it and then blocks waiting for more.
		_, _ = w.Write(bytes.Repeat([]byte("A"), maxDiscoveryBytes))
		flusher.Flush()
		<-release // hold the connection open; never send EOF
	}))
	// Defer order matters: close(release) must run BEFORE srv.Close(), because
	// httptest's Close() blocks until the in-flight handler returns and the
	// handler is parked on <-release. LIFO ⇒ register srv.Close() first.
	defer srv.Close()
	defer close(release)

	done := make(chan error, 1)
	client := NewDiscoveryClient(srv.URL)
	go func() { _, err := client.FetchDiscovery(context.Background()); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error for an all-'A' body read up to the cap")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FetchDiscovery did not return promptly — body read is not bounded")
	}
}
