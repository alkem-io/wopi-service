package config

import (
	"testing"
	"time"
)

func TestDatabaseConfig_DSN(t *testing.T) {
	cfg := DatabaseConfig{
		Host:     "dbhost",
		Port:     "5433",
		Username: "user",
		Password: "pass",
		Name:     "testdb",
		Timeout:  10 * time.Second,
	}
	got := cfg.DSN()
	want := "postgres://user:pass@dbhost:5433/testdb?sslmode=disable&connect_timeout=10"
	if got != want {
		t.Errorf("DSN() = %q, want %q", got, want)
	}
}

func TestLoad_Defaults(t *testing.T) {
	// Set required env var
	t.Setenv("WOPI_TOKEN_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Database.Host != "localhost" {
		t.Errorf("Database.Host = %q, want localhost", cfg.Database.Host)
	}
	if cfg.Database.Port != "5432" {
		t.Errorf("Database.Port = %q, want 5432", cfg.Database.Port)
	}
	if cfg.Database.Name != "wopi" {
		t.Errorf("Database.Name = %q, want wopi", cfg.Database.Name)
	}
	if cfg.NATS.URL != "nats://localhost:4222" {
		t.Errorf("NATS.URL = %q, want nats://localhost:4222", cfg.NATS.URL)
	}
	if cfg.FileService.URL != "http://localhost:4003" {
		t.Errorf("FileService.URL = %q, want http://localhost:4003", cfg.FileService.URL)
	}
	if cfg.ServerPort != "8080" {
		t.Errorf("ServerPort = %q, want 8080", cfg.ServerPort)
	}
	if cfg.TokenSecret != "test-secret" {
		t.Errorf("TokenSecret = %q, want test-secret", cfg.TokenSecret)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("WOPI_TOKEN_SECRET", "secret")
	t.Setenv("WOPI_DATABASE_HOST", "custom-host")
	t.Setenv("WOPI_DATABASE_PORT", "5555")
	t.Setenv("WOPI_SERVER_PORT", "9090")
	t.Setenv("NATS_URL", "nats://custom:4222")
	t.Setenv("FILE_SERVICE_URL", "http://file:5000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Database.Host != "custom-host" {
		t.Errorf("Database.Host = %q, want custom-host", cfg.Database.Host)
	}
	if cfg.Database.Port != "5555" {
		t.Errorf("Database.Port = %q, want 5555", cfg.Database.Port)
	}
	if cfg.ServerPort != "9090" {
		t.Errorf("ServerPort = %q, want 9090", cfg.ServerPort)
	}
	if cfg.NATS.URL != "nats://custom:4222" {
		t.Errorf("NATS.URL = %q, want nats://custom:4222", cfg.NATS.URL)
	}
	if cfg.FileService.URL != "http://file:5000" {
		t.Errorf("FileService.URL = %q, want http://file:5000", cfg.FileService.URL)
	}
}

func TestLoad_MissingRequiredSecret(t *testing.T) {
	t.Setenv("WOPI_TOKEN_SECRET", "")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing WOPI_TOKEN_SECRET")
		}
	}()
	_, _ = Load()
}

func TestLoad_InvalidTimeout(t *testing.T) {
	t.Setenv("WOPI_TOKEN_SECRET", "secret")
	t.Setenv("WOPI_DATABASE_TIMEOUT", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid timeout")
	}
}

func TestParseDuration_PlainSeconds(t *testing.T) {
	d, err := parseDuration("10")
	if err != nil {
		t.Fatalf("parseDuration(\"10\") error: %v", err)
	}
	if d != 10*time.Second {
		t.Errorf("parseDuration(\"10\") = %v, want 10s", d)
	}
}

func TestParseDuration_GoDuration(t *testing.T) {
	d, err := parseDuration("5s")
	if err != nil {
		t.Fatalf("parseDuration(\"5s\") error: %v", err)
	}
	if d != 5*time.Second {
		t.Errorf("parseDuration(\"5s\") = %v, want 5s", d)
	}
}

func TestParseDuration_Invalid(t *testing.T) {
	_, err := parseDuration("abc")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}
