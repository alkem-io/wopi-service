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
	BaseURL         string // Browser-facing URL (editor iframe src)
	CallbackURL     string // Collabora server-side callback URL (WOPISrc); defaults to BaseURL
	FrontendOrigin  string // Origin (scheme://host[:port]) of the page embedding the editor iframe; used as WOPI PostMessageOrigin. Defaults to the origin of BaseURL.
	TokenSecret     string
	ServerPort      string
	ProofValidation bool
	MaxLockLifetime time.Duration // Hard upper bound on how long a single Collabora lockID can persist (via repeated refreshes). A NEW lockID requesting Lock on a file whose existing lock has lived past this is allowed to take over. Defends against zombie DocBrokers that refresh the lock indefinitely.
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

	breakerFailures, breakerTimeout, breakerHalfOpen, err := loadBreakerConfig()
	if err != nil {
		return nil, err
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
		CollaboraURL:   getEnv("WOPI_COLLABORA_URL", "http://localhost:9980"),
		BaseURL:        getEnv("WOPI_BASE_URL", "http://localhost:8080"),
		CallbackURL:    getEnv("WOPI_CALLBACK_URL", ""),
		FrontendOrigin: getEnv("WOPI_FRONTEND_ORIGIN", ""),
		TokenSecret:    getEnv("WOPI_TOKEN_SECRET", ""),
		ServerPort:     getEnv("WOPI_SERVER_PORT", "8080"),
	}

	maxLockLifetime, err := parseDuration(getEnv("WOPI_MAX_LOCK_LIFETIME", "4h"))
	if err != nil {
		return nil, fmt.Errorf("invalid WOPI_MAX_LOCK_LIFETIME: %w", err)
	}
	if maxLockLifetime <= 0 {
		return nil, fmt.Errorf("WOPI_MAX_LOCK_LIFETIME must be positive")
	}
	cfg.MaxLockLifetime = maxLockLifetime

	// Default CallbackURL to BaseURL when not explicitly set
	if cfg.CallbackURL == "" {
		cfg.CallbackURL = cfg.BaseURL
	}

	// FrontendOrigin: explicit value overrides; otherwise derive from BaseURL.
	// Either way, canonicalize through originOf so we always emit a clean
	// scheme://host[:port] string as WOPI PostMessageOrigin — a malformed
	// explicit value (e.g. an accidental trailing path) would otherwise
	// silently break Collabora's PostMessage origin check at runtime.
	source := cfg.FrontendOrigin
	if source == "" {
		source = cfg.BaseURL
	}
	origin, err := originOf(source)
	if err != nil {
		if cfg.FrontendOrigin != "" {
			return nil, fmt.Errorf("invalid WOPI_FRONTEND_ORIGIN: %w", err)
		}
		return nil, fmt.Errorf("derive WOPI_FRONTEND_ORIGIN from WOPI_BASE_URL: %w", err)
	}
	cfg.FrontendOrigin = origin

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

// loadBreakerConfig parses and validates the AUTH_BREAKER_* env vars.
// Extracted to keep Load() within cyclomatic-complexity bounds.
func loadBreakerConfig() (failures uint32, timeoutSecs int, halfOpenMax uint32, err error) {
	failures, err = parseUint32Strict(getEnv("AUTH_BREAKER_FAILURE_THRESHOLD", "3"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid AUTH_BREAKER_FAILURE_THRESHOLD: %w", err)
	}
	if failures == 0 {
		return 0, 0, 0, fmt.Errorf("AUTH_BREAKER_FAILURE_THRESHOLD must be positive")
	}
	timeoutSecs, err = parseIntStrict(getEnv("AUTH_BREAKER_TIMEOUT_SECONDS", "15"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid AUTH_BREAKER_TIMEOUT_SECONDS: %w", err)
	}
	if timeoutSecs <= 0 {
		return 0, 0, 0, fmt.Errorf("AUTH_BREAKER_TIMEOUT_SECONDS must be positive")
	}
	halfOpenMax, err = parseUint32Strict(getEnv("AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS", "2"))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS: %w", err)
	}
	if halfOpenMax == 0 {
		return 0, 0, 0, fmt.Errorf("AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS must be positive")
	}
	return failures, timeoutSecs, halfOpenMax, nil
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

// originOf returns the URL's origin (scheme://host[:port]). Used to
// derive WOPI PostMessageOrigin from BaseURL when not configured
// explicitly. Returns an error for inputs that are not absolute URLs
// with a scheme and host.
func originOf(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL %q lacks scheme or host", raw)
	}
	return u.Scheme + "://" + u.Host, nil
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
