package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestNewWithDefaults(t *testing.T) {
	s, err := NewWithDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s == nil {
		t.Error("expected non-nil server")
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

	t.Run("sets build-time version when config version is empty", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name:      "test-server",
				Transport: "stdio",
			},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
		}

		_, p, err := New(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() {
			if err := p.Close(); err != nil {
				t.Logf("Close() error (non-fatal): %v", err)
			}
		}()

		if cfg.Server.Version != Version {
			t.Errorf("expected version %q, got %q", Version, cfg.Server.Version)
		}
	})

	t.Run("preserves explicit config version", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name:      "test-server",
				Version:   "custom-v1",
				Transport: "stdio",
			},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
		}

		_, p, err := New(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() {
			if err := p.Close(); err != nil {
				t.Logf("Close() error (non-fatal): %v", err)
			}
		}()

		if cfg.Server.Version != "custom-v1" {
			t.Errorf("expected version %q, got %q", "custom-v1", cfg.Server.Version)
		}
	})

	t.Run("refuses a config that sets the retired default_persona", func(t *testing.T) {
		// #1109 retired personas.default_persona and pkg/platform refuses it,
		// but nothing called Config.Validate until #1380, so a deployment that
		// still set the key started and silently denied every caller whose
		// roles matched no persona. The refusal has to happen here, in the
		// composition root, because this is the one place a config reaches the
		// server.
		cfg := &platform.Config{
			Server:   platform.ServerConfig{Name: "test-server", Transport: "stdio"},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
			Personas: platform.PersonasConfig{DefaultPersona: "admin"},
		}

		s, p, err := New(cfg)
		if err == nil {
			if p != nil {
				_ = p.Close()
			}
			t.Fatal("expected an error for a config that sets personas.default_persona")
		}
		if s != nil || p != nil {
			t.Error("expected no server or platform to be built for a refused config")
		}
		if !strings.Contains(err.Error(), "personas.default_persona") {
			t.Errorf("error does not name the offending key: %v", err)
		}
	})

	t.Run("refuses a config file that sets the retired default_persona", func(t *testing.T) {
		// The YAML path reaches the same check: default_persona has a yaml tag
		// so strict decoding accepts it, and LoadConfig applies defaults
		// without validating.
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		configContent := `
server:
  name: test
  transport: stdio
semantic:
  provider: noop
query:
  provider: noop
storage:
  provider: noop
personas:
  admin:
    display_name: Admin
    roles: ["admin"]
    tools:
      allow: ["*"]
  default_persona: admin
`
		if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		_, _, err := NewWithConfig(configPath)
		if err == nil {
			t.Fatal("expected an error for a config file that sets personas.default_persona")
		}
		if !strings.Contains(err.Error(), "personas.default_persona") {
			t.Errorf("error does not name the offending key: %v", err)
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
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
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
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		_, _, err := NewWithConfig(configPath)
		if err == nil {
			t.Error("expected error for invalid config")
		}
	})
}
