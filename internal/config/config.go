// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all service configuration.
type Config struct {
	// Own database (WOPI service state: tokens, locks, sessions)
	Database DatabaseConfig

	// NATS (authorization-evaluation-service)
	NATS NATSConfig

	// file-service-go (file read/write)
	FileService FileServiceConfig

	// Collabora Online
	CollaboraURL string

	// Service
	BaseURL     string
	TokenSecret string
	ServerPort  string
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
		c.Username, c.Password, c.Host, c.Port, c.Name,
		int(c.Timeout.Seconds()),
	)
}

// NATSConfig holds NATS connection parameters.
type NATSConfig struct {
	URL string
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
			URL: getEnv("NATS_URL", "nats://localhost:4222"),
		},
		FileService: FileServiceConfig{
			URL: getEnv("FILE_SERVICE_URL", "http://localhost:4003"),
		},
		CollaboraURL: getEnv("WOPI_COLLABORA_URL", "http://localhost:9980"),
		BaseURL:      getEnv("WOPI_BASE_URL", "http://localhost:8080"),
		TokenSecret:  requireEnv("WOPI_TOKEN_SECRET"),
		ServerPort:   getEnv("WOPI_SERVER_PORT", "8080"),
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
