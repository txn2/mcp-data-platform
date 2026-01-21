package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestNewWithDefaults(t *testing.T) {
	s, toolkit, err := NewWithDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s == nil {
		t.Error("expected non-nil server")
	}

	if toolkit == nil {
		t.Error("expected non-nil toolkit")
	}
}

func TestVersion(t *testing.T) {
	// Version should be set to "dev" by default
	if Version != "dev" {
		t.Errorf("expected Version 'dev', got %q", Version)
	}
}

func TestNew(t *testing.T) {
	t.Run("with valid config", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name:      "test-server",
				Transport: "stdio",
			},
			Semantic: platform.SemanticConfig{
				Provider: "noop",
			},
			Query: platform.QueryConfig{
				Provider: "noop",
			},
			Storage: platform.StorageConfig{
				Provider: "noop",
			},
		}

		s, p, err := New(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Error("expected non-nil server")
		}
		if p == nil {
			t.Error("expected non-nil platform")
		}

		// Clean up
		if err := p.Close(); err != nil {
			t.Logf("Close() error (non-fatal): %v", err)
		}
	})

	t.Run("with invalid semantic provider", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
			Semantic: platform.SemanticConfig{
				Provider: "invalid",
			},
		}

		_, _, err := New(cfg)
		if err == nil {
			t.Error("expected error for invalid semantic provider")
		}
	})
}

func TestNewWithConfig(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		// Create temp config file
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		configContent := `
server:
  name: test-platform
  transport: stdio
semantic:
  provider: noop
query:
  provider: noop
storage:
  provider: noop
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		s, p, err := NewWithConfig(configPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Error("expected non-nil server")
		}
		if p == nil {
			t.Error("expected non-nil platform")
		}

		// Clean up
		if err := p.Close(); err != nil {
			t.Logf("Close() error (non-fatal): %v", err)
		}
	})

	t.Run("missing config file", func(t *testing.T) {
		_, _, err := NewWithConfig("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("expected error for missing config file")
		}
	})

	t.Run("invalid config content", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		// Create config that will fail validation (invalid provider)
		configContent := `
server:
  name: test
semantic:
  provider: unknown-provider
`
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		_, _, err := NewWithConfig(configPath)
		if err == nil {
			t.Error("expected error for invalid config")
		}
	})
}
