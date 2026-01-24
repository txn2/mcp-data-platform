//go:build integration

package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// WaitConfig configures service readiness checks.
type WaitConfig struct {
	Timeout  time.Duration
	Interval time.Duration
}

// DefaultWaitConfig returns default wait configuration.
func DefaultWaitConfig() WaitConfig {
	return WaitConfig{
		Timeout:  60 * time.Second,
		Interval: 2 * time.Second,
	}
}

// WaitForTrino waits for Trino to be ready.
func WaitForTrino(ctx context.Context, address string, cfg WaitConfig) error {
	url := fmt.Sprintf("http://%s/v1/info", address)
	return waitForHTTP(ctx, url, cfg)
}

// WaitForDataHub waits for DataHub GMS to be ready.
func WaitForDataHub(ctx context.Context, baseURL string, cfg WaitConfig) error {
	url := fmt.Sprintf("%s/health", baseURL)
	return waitForHTTP(ctx, url, cfg)
}

// WaitForS3 waits for S3-compatible storage (SeaweedFS) to be ready.
func WaitForS3(ctx context.Context, endpoint string, cfg WaitConfig) error {
	// SeaweedFS S3 API responds to root path
	url := fmt.Sprintf("http://%s/", endpoint)
	return waitForHTTPAny(ctx, url, cfg)
}

// WaitForPostgres waits for PostgreSQL to be ready.
func WaitForPostgres(ctx context.Context, dsn string, cfg WaitConfig) error {
	deadline := time.Now().Add(cfg.Timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		db, err := sql.Open("postgres", dsn)
		if err != nil {
			time.Sleep(cfg.Interval)
			continue
		}

		err = db.PingContext(ctx)
		closeErr := db.Close()
		if err == nil && closeErr == nil {
			return nil
		}

		time.Sleep(cfg.Interval)
	}

	return fmt.Errorf("postgres not ready within %v", cfg.Timeout)
}

// WaitForAllServices waits for all E2E services to be ready.
func WaitForAllServices(ctx context.Context, cfg *E2EConfig, waitCfg WaitConfig) error {
	// Wait for Trino
	if err := WaitForTrino(ctx, cfg.TrinoAddress(), waitCfg); err != nil {
		return fmt.Errorf("trino: %w", err)
	}

	// Wait for S3 (SeaweedFS)
	if err := WaitForS3(ctx, cfg.S3Endpoint, waitCfg); err != nil {
		return fmt.Errorf("s3: %w", err)
	}

	// Wait for PostgreSQL
	if err := WaitForPostgres(ctx, cfg.PostgresDSN, waitCfg); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}

	// Wait for DataHub (optional)
	if cfg.IsDataHubAvailable() {
		if err := WaitForDataHub(ctx, cfg.DataHubURL, waitCfg); err != nil {
			// DataHub is optional - log but don't fail
			fmt.Printf("Warning: DataHub not available: %v\n", err)
		}
	}

	return nil
}

// waitForHTTP waits for an HTTP endpoint to return 200.
func waitForHTTP(ctx context.Context, url string, cfg WaitConfig) error {
	deadline := time.Now().Add(cfg.Timeout)
	client := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(cfg.Interval)
	}

	return fmt.Errorf("service at %s not ready within %v", url, cfg.Timeout)
}

// waitForHTTPAny waits for an HTTP endpoint to respond (any status code).
func waitForHTTPAny(ctx context.Context, url string, cfg WaitConfig) error {
	deadline := time.Now().Add(cfg.Timeout)
	client := &http.Client{Timeout: 5 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			// Any response means the service is up
			return nil
		}

		time.Sleep(cfg.Interval)
	}

	return fmt.Errorf("service at %s not ready within %v", url, cfg.Timeout)
}
