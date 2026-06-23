package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/test"
	"go.uber.org/zap"

	natsadapter "github.com/alkem-io/wopi-service/internal/adapter/outbound/nats"
	"github.com/alkem-io/wopi-service/internal/config"
)

func TestConnectNATS_Success(t *testing.T) {
	srv := natsserver.RunRandClientPortServer()
	defer srv.Shutdown()

	nc, err := connectNATS(srv.ClientURL())
	if err != nil {
		t.Fatalf("connectNATS error: %v", err)
	}
	defer nc.Close()

	if !nc.IsConnected() {
		t.Error("expected connected")
	}
}

func TestConnectNATS_Failure(t *testing.T) {
	_, err := connectNATS("nats://127.0.0.1:1")
	if err == nil {
		t.Error("expected error for unreachable NATS")
	}
}

func TestConnectDB_InvalidDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// pgxpool.New parses lazily, but an entirely broken URL format will fail
	_, err := connectDB(ctx, "not-a-url")
	if err == nil {
		t.Error("expected error for invalid DSN")
	}
}

func TestCreateAdapters(t *testing.T) {
	srv := natsserver.RunRandClientPortServer()
	defer srv.Shutdown()

	nc, err := connectNATS(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	cfg := &config.Config{
		FileService:  config.FileServiceConfig{URL: "http://file:4003"},
		CollaboraURL: "http://collabora:9980",
	}

	// Use nil pool — repos accept DBTX interface, nil won't panic at construction time
	a := createAdapters(nil, natsadapter.NewAuthService(nc), cfg, zap.NewNop())
	if a.tokenRepo == nil {
		t.Error("tokenRepo is nil")
	}
	if a.lockRepo == nil {
		t.Error("lockRepo is nil")
	}
	if a.authSvc == nil {
		t.Error("authSvc is nil")
	}
	if a.fileSvc == nil {
		t.Error("fileSvc is nil")
	}
	if a.discoveryCli == nil {
		t.Error("discoveryCli is nil")
	}
}

func TestCreateServices(t *testing.T) {
	srv := natsserver.RunRandClientPortServer()
	defer srv.Shutdown()

	nc, err := connectNATS(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	cfg := &config.Config{
		FileService:  config.FileServiceConfig{URL: "http://file:4003"},
		CollaboraURL: "http://collabora:9980",
		TokenSecret:  "test-secret",
		BaseURL:      "http://localhost:8080",
	}

	a := createAdapters(nil, natsadapter.NewAuthService(nc), cfg, zap.NewNop())
	s := createServices(a, cfg, zap.NewNop())

	if s.token == nil {
		t.Error("token service is nil")
	}
	if s.wopi == nil {
		t.Error("wopi service is nil")
	}
	if s.discovery == nil {
		t.Error("discovery service is nil")
	}
	if s.cleanup == nil {
		t.Error("cleanup service is nil")
	}
}

func TestCreateHandlers(t *testing.T) {
	srv := natsserver.RunRandClientPortServer()
	defer srv.Shutdown()

	nc, err := connectNATS(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	cfg := &config.Config{
		FileService:  config.FileServiceConfig{URL: "http://file:4003"},
		CollaboraURL: "http://collabora:9980",
		TokenSecret:  "test-secret",
		BaseURL:      "http://localhost:8080",
	}

	a := createAdapters(nil, natsadapter.NewAuthService(nc), cfg, zap.NewNop())
	s := createServices(a, cfg, zap.NewNop())
	h := createHandlers(s, nil, nc, zap.NewNop())

	if h.token == nil {
		t.Error("token handler is nil")
	}
	if h.wopi == nil {
		t.Error("wopi handler is nil")
	}
	if h.health == nil {
		t.Error("health handler is nil")
	}
	if h.discovery == nil {
		t.Error("discovery handler is nil")
	}
}

func TestNewHTTPServer(t *testing.T) {
	srv := newHTTPServer("9999", http.NewServeMux())
	if srv.Addr != ":9999" {
		t.Errorf("Addr = %q, want :9999", srv.Addr)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 60*time.Second {
		t.Errorf("WriteTimeout = %v", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v", srv.IdleTimeout)
	}
	if srv.Handler == nil {
		t.Error("Handler is nil")
	}
}

func TestRunMigrations_InvalidDSN(t *testing.T) {
	err := runMigrations("postgres://invalid:1/nonexistent?sslmode=disable&connect_timeout=1", zap.NewNop())
	if err == nil {
		t.Error("expected error for invalid DSN")
	}
}
