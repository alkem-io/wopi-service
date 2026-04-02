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
			URL:                getEnv("AUTH_SERVICE_URL", "http://authorization-evaluation-service:6060"),
			BreakerFailures:    parseUint32(getEnv("AUTH_BREAKER_FAILURE_THRESHOLD", "3")),
			BreakerTimeoutSecs: parseInt(getEnv("AUTH_BREAKER_TIMEOUT_SECONDS", "15")),
			BreakerHalfOpenMax: parseUint32(getEnv("AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS", "2")),
		},
		FileService: FileServiceConfig{
			URL: getEnv("FILE_SERVICE_URL", "http://localhost:4003"),
		},
		CollaboraURL:    getEnv("WOPI_COLLABORA_URL", "http://localhost:9980"),
		BaseURL:         getEnv("WOPI_BASE_URL", "http://localhost:8080"),
		TokenSecret:     requireEnv("WOPI_TOKEN_SECRET"),
		ServerPort:      getEnv("WOPI_SERVER_PORT", "8080"),
		ProofValidation: getEnv("WOPI_PROOF_VALIDATION", "true") == "true",
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %s is not set", key))
	}
	return v
}

func parseUint32(s string) uint32 {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func parseInt(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
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
