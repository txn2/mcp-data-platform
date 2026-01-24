//go:build integration

// Package helpers provides test utilities for E2E testing.
package helpers

import (
	"os"
	"strconv"
	"time"
)

// E2EConfig holds configuration for E2E tests.
type E2EConfig struct {
	// Trino configuration
	TrinoHost string
	TrinoPort int

	// DataHub configuration
	DataHubURL   string
	DataHubToken string

	// PostgreSQL configuration
	PostgresDSN string

	// S3-compatible storage configuration (SeaweedFS)
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Region    string

	// Test configuration
	Timeout time.Duration
}

// DefaultE2EConfig returns E2E configuration from environment variables with defaults.
func DefaultE2EConfig() *E2EConfig {
	return &E2EConfig{
		// Trino
		TrinoHost: getEnv("E2E_TRINO_HOST", "localhost"),
		TrinoPort: getEnvInt("E2E_TRINO_PORT", 8090),

		// DataHub
		DataHubURL:   getEnv("E2E_DATAHUB_URL", "http://localhost:8080"),
		DataHubToken: getEnv("E2E_DATAHUB_TOKEN", ""),

		// PostgreSQL
		PostgresDSN: getEnv("E2E_POSTGRES_DSN", "postgres://platform:platform_secret@localhost:5432/mcp_platform?sslmode=disable"),

		// S3 (SeaweedFS)
		S3Endpoint:  getEnv("E2E_S3_ENDPOINT", "localhost:9000"),
		S3AccessKey: getEnv("E2E_S3_ACCESS_KEY", "admin"),
		S3SecretKey: getEnv("E2E_S3_SECRET_KEY", "admin_secret"),
		S3Region:    getEnv("E2E_S3_REGION", "us-east-1"),

		// Test timeouts
		Timeout: getEnvDuration("E2E_TIMEOUT", 30*time.Second),
	}
}

// TrinoAddress returns the Trino address in host:port format.
func (c *E2EConfig) TrinoAddress() string {
	return c.TrinoHost + ":" + strconv.Itoa(c.TrinoPort)
}

// IsDataHubAvailable checks if DataHub configuration is present.
func (c *E2EConfig) IsDataHubAvailable() bool {
	return c.DataHubURL != ""
}

// getEnv returns the environment variable value or a default.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt returns the environment variable as an int or a default.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// getEnvDuration returns the environment variable as a duration or a default.
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

// SkipIfDataHubSearchUnavailable checks if DataHub search is working.
// Returns true if search is unavailable and test should be skipped.
func SkipIfDataHubSearchUnavailable(cfg *E2EConfig) bool {
	if !cfg.IsDataHubAvailable() {
		return true
	}
	// Skip if E2E_SKIP_SEARCH_TESTS is set (for when OpenSearch is not running)
	if os.Getenv("E2E_SKIP_SEARCH_TESTS") == "true" {
		return true
	}
	return false
}
