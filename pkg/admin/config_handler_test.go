package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestGetConfig(t *testing.T) {
	t.Run("returns redacted config", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name:      "test-platform",
				Transport: "http",
			},
			Database: platform.DatabaseConfig{
				DSN: "postgres://user:pass@localhost/db",
			},
			Auth: platform.AuthConfig{
				APIKeys: platform.APIKeyAuthConfig{
					Enabled: true,
					Keys: []platform.APIKeyDef{
						{Key: "super-secret-key", Name: "admin", Roles: []string{"admin"}},
					},
				},
			},
			OAuth: platform.OAuthConfig{
				SigningKey: "base64-signing-key",
			},
		}
		h := NewHandler(Deps{Config: cfg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))

		// Server name should be visible
		server, ok := body["server"].(map[string]any)
		require.True(t, ok, "server should be a map")
		assert.Equal(t, "test-platform", server["name"])

		// DSN should be redacted
		db, ok := body["database"].(map[string]any)
		require.True(t, ok, "database should be a map")
		assert.Equal(t, "[REDACTED]", db["dsn"])

		// Signing key should be redacted
		oauth, ok := body["oauth"].(map[string]any)
		require.True(t, ok, "oauth should be a map")
		assert.Equal(t, "[REDACTED]", oauth["signing_key"])

		// API keys should be redacted
		auth, ok := body["auth"].(map[string]any)
		require.True(t, ok, "auth should be a map")
		apiKeys, ok := auth["api_keys"].(map[string]any)
		require.True(t, ok, "api_keys should be a map")
		keys, ok := apiKeys["keys"].([]any)
		require.True(t, ok, "keys should be a slice")
		firstKey, ok := keys[0].(map[string]any)
		require.True(t, ok, "first key should be a map")
		assert.Equal(t, "[REDACTED]", firstKey["key"])
	})
}

func TestConfigMode(t *testing.T) {
	t.Run("returns file mode when no config store", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "file", body["mode"])
		assert.Equal(t, true, body["read_only"])
	})

	t.Run("returns file mode with file config store", func(t *testing.T) {
		h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "file"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "file", body["mode"])
		assert.Equal(t, true, body["read_only"])
	})

	t.Run("returns database mode", func(t *testing.T) {
		h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "database"}}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "database", body["mode"])
		assert.Equal(t, false, body["read_only"])
	})
}

func TestExportConfig(t *testing.T) {
	t.Run("exports redacted YAML by default", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name:      "test-platform",
				Transport: "http",
			},
			Database: platform.DatabaseConfig{
				DSN: "postgres://user:pass@localhost/db",
			},
		}
		h := NewHandler(Deps{Config: cfg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/export", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/x-yaml", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Header().Get("Content-Disposition"), "platform-config.yaml")

		body := w.Body.String()
		assert.Contains(t, body, "test-platform")
		assert.Contains(t, body, "[REDACTED]")
		assert.NotContains(t, body, "postgres://user:pass@localhost/db")
	})

	t.Run("exports unredacted YAML with secrets=true", func(t *testing.T) {
		cfg := &platform.Config{
			Server: platform.ServerConfig{
				Name: "test-platform",
			},
			Database: platform.DatabaseConfig{
				DSN: "postgres://user:pass@localhost/db",
			},
		}
		h := NewHandler(Deps{Config: cfg}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/export?secrets=true", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "postgres://user:pass@localhost/db")
		assert.NotContains(t, body, "[REDACTED]")
	})

	t.Run("returns error when no config", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/export", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestListConfigEntries(t *testing.T) {
	t.Run("returns entries", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			entries: map[string]*configstore.Entry{
				"server.description": {
					Key:       "server.description",
					Value:     "Test platform",
					UpdatedBy: "admin",
					UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/entries", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var entries []configstore.Entry
		require.NoError(t, json.NewDecoder(w.Body).Decode(&entries))
		assert.Len(t, entries, 1)
		assert.Equal(t, "server.description", entries[0].Key)
	})
}

func TestGetConfigEntry(t *testing.T) {
	t.Run("returns entry", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			entries: map[string]*configstore.Entry{
				"server.description": {
					Key:   "server.description",
					Value: "Hello",
				},
			},
		}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/entries/server.description", http.NoBody)
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var entry configstore.Entry
		require.NoError(t, json.NewDecoder(w.Body).Decode(&entry))
		assert.Equal(t, "server.description", entry.Key)
		assert.Equal(t, "Hello", entry.Value)
	})

	t.Run("returns 404 for missing entry", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/entries/server.description", http.NoBody)
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestSetConfigEntry(t *testing.T) {
	t.Run("sets whitelisted key", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		cfg := testConfig()
		h := NewHandler(Deps{ConfigStore: cs, Config: cfg}, nil)

		body := `{"value":"My Platform"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/config/entries/server.description", strings.NewReader(body))
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, cs.setCalls)
		// Verify hot-reload applied
		assert.Equal(t, "My Platform", cfg.Server.Description)
	})

	t.Run("rejects non-whitelisted key", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		body := `{"value":"bad"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/config/entries/database.dsn", strings.NewReader(body))
		req.SetPathValue("key", "database.dsn")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, 0, cs.setCalls)
	})

	t.Run("rejects invalid body", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/config/entries/server.description", strings.NewReader("{bad"))
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDeleteConfigEntry(t *testing.T) {
	t.Run("deletes existing entry", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			entries: map[string]*configstore.Entry{
				"server.description": {Key: "server.description", Value: "old"},
			},
		}
		cfg := testConfig()
		cfg.Server.Description = "overridden"
		h := NewHandler(Deps{
			ConfigStore:  cs,
			Config:       cfg,
			FileDefaults: map[string]string{"server.description": "file-default"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/config/entries/server.description", http.NoBody)
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		// Should revert to file default
		assert.Equal(t, "file-default", cfg.Server.Description)
	})

	t.Run("returns 404 for missing entry", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/config/entries/server.description", http.NoBody)
		req.SetPathValue("key", "server.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestGetConfigChangelog(t *testing.T) {
	t.Run("returns changelog entries", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			changelog: []configstore.ChangelogEntry{
				{
					ID:        1,
					Key:       "server.description",
					Action:    "set",
					ChangedBy: "admin",
					ChangedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/changelog", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var entries []configstore.ChangelogEntry
		require.NoError(t, json.NewDecoder(w.Body).Decode(&entries))
		assert.Len(t, entries, 1)
		assert.Equal(t, "server.description", entries[0].Key)
	})
}

func TestRedactYAML(t *testing.T) {
	t.Run("redacts sensitive values in YAML", func(t *testing.T) {
		input := []byte("database:\n  dsn: postgres://user:pass@host/db\nserver:\n  name: test\n")
		result, err := redactYAML(input)
		require.NoError(t, err)
		assert.Contains(t, string(result), "[REDACTED]")
		assert.NotContains(t, string(result), "postgres://")
		assert.Contains(t, string(result), "test")
	})

	t.Run("returns error for invalid YAML", func(t *testing.T) {
		_, err := redactYAML([]byte("{{invalid"))
		assert.Error(t, err)
	})
}

func TestIsMutable(t *testing.T) {
	t.Run("false when config store is nil", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)
		assert.False(t, h.isMutable())
	})

	t.Run("false in file mode", func(t *testing.T) {
		h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "file"}}, nil)
		assert.False(t, h.isMutable())
	})

	t.Run("true in database mode", func(t *testing.T) {
		h := NewHandler(Deps{ConfigStore: &mockConfigStore{mode: "database"}}, nil)
		assert.True(t, h.isMutable())
	})
}

func TestConfigToMap(t *testing.T) {
	t.Run("converts struct to map", func(t *testing.T) {
		type simple struct {
			Name string `yaml:"name"`
			Port int    `yaml:"port"`
		}
		m, err := configToMap(&simple{Name: "test", Port: 8080})
		require.NoError(t, err)
		assert.Equal(t, "test", m["name"])
		assert.Equal(t, 8080, m["port"])
	})

	t.Run("converts nil to nil map", func(t *testing.T) {
		m, err := configToMap(nil)
		require.NoError(t, err)
		assert.Nil(t, m)
	})
}

func TestRedactMap(t *testing.T) {
	t.Run("redacts sensitive keys", func(t *testing.T) {
		m := map[string]any{
			"name":              "visible",
			"key":               "secret-api-key",
			"password":          "secret-password",
			"token":             "secret-token",
			"dsn":               "postgres://user:pass@host/db",
			"secret":            "top-secret",
			"signing_key":       "signing-key-value",
			"secret_access_key": "aws-key",
			"client_secret":     "oauth-secret",
		}
		redactMap(m)

		assert.Equal(t, "visible", m["name"])
		assert.Equal(t, "[REDACTED]", m["key"])
		assert.Equal(t, "[REDACTED]", m["password"])
		assert.Equal(t, "[REDACTED]", m["token"])
		assert.Equal(t, "[REDACTED]", m["dsn"])
		assert.Equal(t, "[REDACTED]", m["secret"])
		assert.Equal(t, "[REDACTED]", m["signing_key"])
		assert.Equal(t, "[REDACTED]", m["secret_access_key"])
		assert.Equal(t, "[REDACTED]", m["client_secret"])
	})

	t.Run("redacts nested maps", func(t *testing.T) {
		m := map[string]any{
			"database": map[string]any{
				"dsn":      "postgres://...",
				"max_pool": 10,
			},
		}
		redactMap(m)

		db, ok := m["database"].(map[string]any)
		require.True(t, ok, "database should be a map")
		assert.Equal(t, "[REDACTED]", db["dsn"])
		assert.Equal(t, 10, db["max_pool"])
	})

	t.Run("redacts in slices", func(t *testing.T) {
		m := map[string]any{
			"keys": []any{
				map[string]any{"key": "secret1", "name": "admin"},
				map[string]any{"key": "secret2", "name": "viewer"},
			},
		}
		redactMap(m)

		keys, ok := m["keys"].([]any)
		require.True(t, ok, "keys should be a slice")
		first, ok := keys[0].(map[string]any)
		require.True(t, ok, "first key should be a map")
		assert.Equal(t, "[REDACTED]", first["key"])
		assert.Equal(t, "admin", first["name"])
	})

	t.Run("does not redact empty sensitive values", func(t *testing.T) {
		m := map[string]any{
			"password": "",
			"token":    "",
		}
		redactMap(m)

		assert.Equal(t, "", m["password"])
		assert.Equal(t, "", m["token"])
	})

	t.Run("handles nil map gracefully", func(_ *testing.T) {
		// Should not panic
		redactMap(nil)
	})
}

func TestIsSensitiveKey(t *testing.T) {
	tests := []struct {
		key      string
		expected bool
	}{
		{"key", true},
		{"password", true},
		{"token", true},
		{"dsn", true},
		{"secret", true},
		{"signing_key", true},
		{"secret_access_key", true},
		{"client_secret", true},
		{"KEY", true},   // case insensitive
		{"Token", true}, // case insensitive
		{"name", false},
		{"host", false},
		{"port", false},
		{"enabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.expected, isSensitiveKey(tt.key))
		})
	}
}

// --- File mode blocking tests ---

func TestFileMode_PersonaMutationsBlocked(t *testing.T) {
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin")}
	// No ConfigStore → file mode (routes not registered)
	h := NewHandler(Deps{PersonaRegistry: pReg, Config: testConfig()}, nil)

	t.Run("POST persona returns 405", func(t *testing.T) {
		body := `{"name":"new","display_name":"New"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("PUT persona returns 405", func(t *testing.T) {
		body := `{"display_name":"Updated"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/admin", strings.NewReader(body))
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("DELETE persona returns 405", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/admin", http.NoBody)
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET personas still works", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/personas", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestFileMode_AuthKeyMutationsBlocked(t *testing.T) {
	mgr := &mockAPIKeyManager{}
	// No ConfigStore → file mode (routes not registered)
	h := NewHandler(Deps{APIKeyManager: mgr, PersonaRegistry: &mockPersonaRegistry{}}, nil)

	t.Run("POST auth key returns 405", func(t *testing.T) {
		body := `{"name":"new","roles":["admin"]}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("DELETE auth key returns 405", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/test", http.NoBody)
		req.SetPathValue("name", "test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET auth keys still works", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestListEffectiveConfig(t *testing.T) {
	t.Run("returns file defaults when no DB overrides", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{
			ConfigStore: cs,
			Config:      testConfig(),
			FileDefaults: map[string]string{
				"server.description":        "file desc",
				"server.agent_instructions": "file instructions",
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var result []effectiveConfigEntry
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Len(t, result, 2)
		// Should be sorted by key
		assert.Equal(t, "server.agent_instructions", result[0].Key)
		assert.Equal(t, "file", result[0].Source)
		assert.Equal(t, "file instructions", result[0].Value)
		assert.Equal(t, "server.description", result[1].Key)
		assert.Equal(t, "file", result[1].Source)
		assert.Equal(t, "file desc", result[1].Value)
	})

	t.Run("DB override replaces file default", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			entries: map[string]*configstore.Entry{
				"server.description": {
					Key:       "server.description",
					Value:     "db desc",
					UpdatedBy: "admin@test.com",
					UpdatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		}
		h := NewHandler(Deps{
			ConfigStore: cs,
			Config:      testConfig(),
			FileDefaults: map[string]string{
				"server.description":        "file desc",
				"server.agent_instructions": "file instructions",
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var result []effectiveConfigEntry
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Len(t, result, 2)
		// agent_instructions from file, description from DB
		assert.Equal(t, "server.agent_instructions", result[0].Key)
		assert.Equal(t, "file", result[0].Source)
		assert.Equal(t, "server.description", result[1].Key)
		assert.Equal(t, "database", result[1].Source)
		assert.Equal(t, "db desc", result[1].Value)
		assert.NotNil(t, result[1].UpdatedBy)
		assert.Equal(t, "admin@test.com", *result[1].UpdatedBy)
	})

	t.Run("returns 500 on DB error", func(t *testing.T) {
		cs := &mockConfigStore{
			mode:    "database",
			listErr: fmt.Errorf("connection refused"),
		}
		h := NewHandler(Deps{
			ConfigStore: cs,
			Config:      testConfig(),
			FileDefaults: map[string]string{
				"server.description": "file desc",
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/config/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestSetConfigEntry_ToolDescriptionOverride(t *testing.T) {
	makeHandler := func() (*Handler, *platform.Config, *mockConfigStore) {
		cs := &mockConfigStore{mode: "database"}
		cfg := testConfig()
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
			},
		}
		h := NewHandler(Deps{ConfigStore: cs, Config: cfg, ToolkitRegistry: reg}, nil)
		return h, cfg, cs
	}

	t.Run("accepts tool.<known>.description and applies hot-reload", func(t *testing.T) {
		h, cfg, cs := makeHandler()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/config/entries/tool.trino_query.description",
			strings.NewReader(`{"value":"custom desc"}`))
		req.SetPathValue("key", "tool.trino_query.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, cs.setCalls)
		require.NotNil(t, cfg.Tools.DescriptionOverrides)
		assert.Equal(t, "custom desc", cfg.Tools.DescriptionOverrides["trino_query"])
	})

	t.Run("rejects tool.<unknown>.description", func(t *testing.T) {
		h, _, cs := makeHandler()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/config/entries/tool.no_such_tool.description",
			strings.NewReader(`{"value":"x"}`))
		req.SetPathValue("key", "tool.no_such_tool.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, 0, cs.setCalls)
	})

	t.Run("rejects tool description key without registry", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/config/entries/tool.trino_query.description",
			strings.NewReader(`{"value":"x"}`))
		req.SetPathValue("key", "tool.trino_query.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("accepts tool.<platform-tool>.description", func(t *testing.T) {
		// Platform-level tools (platform_info, list_connections, manage_prompt)
		// aren't owned by any toolkit, so they aren't in ToolkitRegistry.AllTools.
		// The whitelist must still accept their description-override keys.
		cs := &mockConfigStore{mode: "database"}
		cfg := testConfig()
		h := NewHandler(Deps{
			ConfigStore: cs,
			Config:      cfg,
			ToolkitRegistry: &mockToolkitRegistry{
				allResult: []mockToolkit{},
			},
			PlatformTools: []platform.ToolInfo{
				{Name: "platform_info", Kind: "platform"},
			},
		}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/config/entries/tool.platform_info.description",
			strings.NewReader(`{"value":"custom"}`))
		req.SetPathValue("key", "tool.platform_info.description")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "custom", cfg.Tools.DescriptionOverrides["platform_info"])
	})
}

func TestDeleteConfigEntry_ToolDescriptionOverride(t *testing.T) {
	cs := &mockConfigStore{
		mode: "database",
		entries: map[string]*configstore.Entry{
			"tool.trino_query.description": {Key: "tool.trino_query.description", Value: "old"},
		},
	}
	cfg := testConfig()
	cfg.Tools.DescriptionOverrides = map[string]string{"trino_query": "old"}
	reg := &mockToolkitRegistry{
		allResult: []mockToolkit{
			{kind: "trino", name: "prod", connection: "prod-trino", tools: []string{"trino_query"}},
		},
	}
	h := NewHandler(Deps{ConfigStore: cs, Config: cfg, ToolkitRegistry: reg}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/config/entries/tool.trino_query.description", http.NoBody)
	req.SetPathValue("key", "tool.trino_query.description")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	_, exists := cfg.Tools.DescriptionOverrides["trino_query"]
	assert.False(t, exists, "override should be removed from live config after delete")
}

func TestExtractToolNameFromDescriptionKey(t *testing.T) {
	tests := []struct {
		key      string
		wantName string
		wantOK   bool
	}{
		{"tool.trino_query.description", "trino_query", true},
		{"tool.dev-mock__echo.description", "dev-mock__echo", true},
		{"server.description", "", false},
		{"tool..description", "", false}, // empty name guarded by length check
		{"tool.x.descriptionz", "", false},
		{"prefix.tool.x.description", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			gotName, gotOK := extractToolNameFromDescriptionKey(tc.key)
			assert.Equal(t, tc.wantName, gotName)
			assert.Equal(t, tc.wantOK, gotOK)
		})
	}
}
