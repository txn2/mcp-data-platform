package admin

import (
	"context"
	"encoding/json"
	"errors"
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

var errTestSave = errors.New("save failed")

const testValidConfigYAML = "apiVersion: v1\nserver:\n  name: imported\n  transport: stdio\n"

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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/mode", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/export", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/export?secrets=true", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "postgres://user:pass@localhost/db")
		assert.NotContains(t, body, "[REDACTED]")
	})

	t.Run("returns error when no config", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/export", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestImportConfig(t *testing.T) {
	t.Run("imports valid config in database mode", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		yamlBody := testValidConfigYAML
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import?comment=test+import", strings.NewReader(yamlBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "saved", body["status"])
		assert.Contains(t, body["note"], "next restart")
		assert.Equal(t, 1, cs.saveCalls)
	})

	t.Run("rejects invalid YAML", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import", strings.NewReader("{{invalid yaml"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, 0, cs.saveCalls)
	})

	t.Run("rejects config that fails validation", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		// OIDC enabled without issuer fails validation
		yamlBody := "apiVersion: v1\nauth:\n  oidc:\n    enabled: true\n"
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import", strings.NewReader(yamlBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "validation failed")
		assert.Equal(t, 0, cs.saveCalls)
	})

	t.Run("imports with authenticated user", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		yamlBody := testValidConfigYAML
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import?comment=user+import", strings.NewReader(yamlBody))
		ctx := context.WithValue(req.Context(), adminUserKey, &User{UserID: "admin-user"})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, cs.saveCalls)
	})

	t.Run("returns error on save failure", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database", saveErr: errTestSave}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		yamlBody := testValidConfigYAML
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import", strings.NewReader(yamlBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("blocks import in file mode", func(t *testing.T) {
		cs := &mockConfigStore{mode: "file"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/import", strings.NewReader("apiVersion: v1\n"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "config is read-only in file mode", pd.Detail)
	})
}

func TestConfigHistory(t *testing.T) {
	t.Run("returns revisions in database mode", func(t *testing.T) {
		cs := &mockConfigStore{
			mode: "database",
			history: []configstore.Revision{
				{
					ID:        1,
					Version:   1,
					Author:    "admin",
					Comment:   "initial",
					CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/history", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(1), body["total"])
		revisions, ok := body["revisions"].([]any)
		require.True(t, ok, "revisions should be a slice")
		assert.Len(t, revisions, 1)
	})

	t.Run("returns empty revisions", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/history", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
	})

	t.Run("returns error on history failure", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		cs.historyErr = errTestSave
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/history", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("blocks history in file mode", func(t *testing.T) {
		cs := &mockConfigStore{mode: "file"}
		h := NewHandler(Deps{ConfigStore: cs, Config: testConfig()}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/history", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
	})
}

func TestSyncConfig(t *testing.T) {
	t.Run("no-op when config store is nil", func(_ *testing.T) {
		h := NewHandler(Deps{Config: testConfig()}, nil)
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		// Should not panic
		h.syncConfig(req, "test")
	})

	t.Run("no-op in file mode", func(t *testing.T) {
		cs := &mockConfigStore{mode: "file"}
		h := NewHandler(Deps{Config: testConfig(), ConfigStore: cs}, nil)
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		h.syncConfig(req, "test")
		assert.Equal(t, 0, cs.saveCalls)
	})

	t.Run("saves in database mode", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{Config: testConfig(), ConfigStore: cs}, nil)
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		h.syncConfig(req, "test mutation")
		assert.Equal(t, 1, cs.saveCalls)
	})

	t.Run("saves with user context", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database"}
		h := NewHandler(Deps{Config: testConfig(), ConfigStore: cs}, nil)
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		ctx := context.WithValue(req.Context(), adminUserKey, &User{UserID: "admin-user"})
		req = req.WithContext(ctx)
		h.syncConfig(req, "with user")
		assert.Equal(t, 1, cs.saveCalls)
	})

	t.Run("logs error on save failure", func(t *testing.T) {
		cs := &mockConfigStore{mode: "database", saveErr: errTestSave}
		h := NewHandler(Deps{Config: testConfig(), ConfigStore: cs}, nil)
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		// Should not panic, error is logged
		h.syncConfig(req, "fail save")
		assert.Equal(t, 1, cs.saveCalls)
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("PUT persona returns 405", func(t *testing.T) {
		body := `{"display_name":"Updated"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/personas/admin", strings.NewReader(body))
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("DELETE persona returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/personas/admin", http.NoBody)
		req.SetPathValue("name", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET personas still works", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/personas", http.NoBody)
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("DELETE auth key returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/auth/keys/test", http.NoBody)
		req.SetPathValue("name", "test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET auth keys still works", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/auth/keys", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}
