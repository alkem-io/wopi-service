// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Config holds all service configuration.
type Config struct {
	// Own database (WOPI service state: tokens, locks, sessions)
	Database DatabaseConfig

	// Authorization evaluation service (NATS or h2c — mutually exclusive)
	NATS    NATSConfig
	AuthSvc AuthSvcConfig

	// file-service-go (file read/write)
	FileService FileServiceConfig

	// Collabora Online
	CollaboraURL string

	// Service
	BaseURL         string
	TokenSecret     string
	ServerPort      string
	ProofValidation bool
}

// DatabaseConfig holds PostgreSQL connection parameters.
type DatabaseConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Name     string
	Timeout  time.Duration
}

// DSN returns the PostgreSQL connection string.
// sslmode=disable: TLS is handled at the network level (K8s service mesh),
// not at the PostgreSQL connection level.
func (c DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable&connect_timeout=%d",
		url.QueryEscape(c.Username), url.QueryEscape(c.Password),
		c.Host, c.Port, c.Name, int(c.Timeout.Seconds()),
	)
}

// NATSConfig holds NATS connection parameters. URL is empty when h2c transport is used.
type NATSConfig struct {
	URL string
}

// AuthSvcConfig holds authorization-evaluation-service connection parameters.
type AuthSvcConfig struct {
	URL                string
	BreakerFailures    uint32
	BreakerTimeoutSecs int
	BreakerHalfOpenMax uint32
}

// FileServiceConfig holds file-service-go connection parameters.
type FileServiceConfig struct {
	URL string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	dbTimeout, err := parseDuration(getEnv("WOPI_DATABASE_TIMEOUT", "5s"))
	if err != nil {
		return nil, fmt.Errorf("invalid WOPI_DATABASE_TIMEOUT: %w", err)
	}
	if dbTimeout <= 0 {
		return nil, fmt.Errorf("WOPI_DATABASE_TIMEOUT must be positive")
	}

	breakerFailures, err := parseUint32Strict(getEnv("AUTH_BREAKER_FAILURE_THRESHOLD", "3"))
	if err != nil {
		return nil, fmt.Errorf("invalid AUTH_BREAKER_FAILURE_THRESHOLD: %w", err)
	}
	if breakerFailures == 0 {
		return nil, fmt.Errorf("AUTH_BREAKER_FAILURE_THRESHOLD must be positive")
	}
	breakerTimeout, err := parseIntStrict(getEnv("AUTH_BREAKER_TIMEOUT_SECONDS", "15"))
	if err != nil {
		return nil, fmt.Errorf("invalid AUTH_BREAKER_TIMEOUT_SECONDS: %w", err)
	}
	if breakerTimeout <= 0 {
		return nil, fmt.Errorf("AUTH_BREAKER_TIMEOUT_SECONDS must be positive")
	}
	breakerHalfOpen, err := parseUint32Strict(getEnv("AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS", "2"))
	if err != nil {
		return nil, fmt.Errorf("invalid AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS: %w", err)
	}
	if breakerHalfOpen == 0 {
		return nil, fmt.Errorf("AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS must be positive")
	}

	cfg := &Config{
		Database: DatabaseConfig{
			Host:     getEnv("WOPI_DATABASE_HOST", "localhost"),
			Port:     getEnv("WOPI_DATABASE_PORT", "5432"),
			Username: getEnv("WOPI_DATABASE_USERNAME", "postgres"),
			Password: getEnv("WOPI_DATABASE_PASSWORD", "postgres"),
			Name:     getEnv("WOPI_DATABASE_NAME", "wopi"),
			Timeout:  dbTimeout,
		},
		NATS: NATSConfig{
			URL: getEnv("NATS_URL", ""),
		},
		AuthSvc: AuthSvcConfig{
			URL:                getEnv("AUTH_SERVICE_URL", ""),
			BreakerFailures:    breakerFailures,
			BreakerTimeoutSecs: breakerTimeout,
			BreakerHalfOpenMax: breakerHalfOpen,
		},
		FileService: FileServiceConfig{
			URL: getEnv("FILE_SERVICE_URL", "http://localhost:4003"),
		},
		CollaboraURL: getEnv("WOPI_COLLABORA_URL", "http://localhost:9980"),
		BaseURL:      getEnv("WOPI_BASE_URL", "http://localhost:8080"),
		TokenSecret:  getEnv("WOPI_TOKEN_SECRET", ""),
		ServerPort:   getEnv("WOPI_SERVER_PORT", "8080"),
	}

	if cfg.TokenSecret == "" {
		return nil, fmt.Errorf("required environment variable WOPI_TOKEN_SECRET is not set")
	}

	proofVal := getEnv("WOPI_PROOF_VALIDATION", "true")
	switch proofVal {
	case "true":
		cfg.ProofValidation = true
	case "false":
		cfg.ProofValidation = false
	default:
		return nil, fmt.Errorf("WOPI_PROOF_VALIDATION must be \"true\" or \"false\", got %q", proofVal)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseUint32Strict(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as uint32: %w", s, err)
	}
	return uint32(v), nil
}

func parseIntStrict(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as int: %w", s, err)
	}
	return v, nil
}

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		// Try parsing as plain seconds
		secs, numErr := strconv.Atoi(s)
		if numErr != nil {
			return 0, fmt.Errorf("cannot parse %q as duration: %w", s, err)
		}
		return time.Duration(secs) * time.Second, nil
	}
	return d, nil
}
