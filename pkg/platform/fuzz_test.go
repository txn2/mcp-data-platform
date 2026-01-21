package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoadConfig fuzzes YAML config loading.
func FuzzLoadConfig(f *testing.F) {
	// Seed with various YAML structures
	f.Add(`server:
  name: test
  transport: stdio`)

	f.Add(`server:
  name: test
semantic:
  provider: noop
query:
  provider: noop
storage:
  provider: noop`)

	f.Add(`{}`)
	f.Add(`null`)
	f.Add(`server: null`)
	f.Add(`server:
  name: [1, 2, 3]`) // wrong type

	f.Add(`auth:
  oidc:
    enabled: true
    issuer: https://example.com
  api_keys:
    enabled: true
    keys:
      - key: test
        name: test
        roles: [admin]`)

	f.Add(`personas:
  definitions:
    analyst:
      display_name: Analyst
      roles: [analyst]
      tools:
        allow: ["*"]`)

	f.Add(`toolkits:
  trino:
    - name: prod
      config:
        host: localhost
        port: 8080`)

	// Deeply nested structure
	f.Add(`a:
  b:
    c:
      d:
        e: value`)

	f.Fuzz(func(t *testing.T, yamlContent string) {
		// Create temp file
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
			return
		}

		// Should never panic
		_, _ = LoadConfig(configPath)
	})
}

// FuzzExpandEnv fuzzes environment variable expansion in config.
func FuzzExpandEnv(f *testing.F) {
	f.Add("${HOME}")
	f.Add("${NONEXISTENT_VAR}")
	f.Add("${}")
	f.Add("$HOME")
	f.Add("prefix${VAR}suffix")
	f.Add("${VAR1}${VAR2}")
	f.Add("no-vars-here")
	f.Add("$$escaped")
	f.Add("${VAR:-default}")

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		_ = os.ExpandEnv(input)
	})
}

// FuzzServerConfig fuzzes server configuration parsing.
func FuzzServerConfig(f *testing.F) {
	f.Add("test-server", "stdio", ":8080")
	f.Add("", "", "")
	f.Add("server", "sse", ":0")
	f.Add("server", "invalid", "not-an-address")

	f.Fuzz(func(t *testing.T, name, transport, address string) {
		cfg := &Config{
			Server: ServerConfig{
				Name:      name,
				Transport: transport,
				Address:   address,
			},
			Semantic: SemanticConfig{Provider: "noop"},
			Query:    QueryConfig{Provider: "noop"},
			Storage:  StorageConfig{Provider: "noop"},
		}

		// Should never panic when creating platform
		p, err := New(WithConfig(cfg))
		if err != nil {
			return
		}
		_ = p.Close()
	})
}
