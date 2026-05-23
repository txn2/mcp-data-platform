package admin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// --- Mock ConnectionStore ---

type mockConnectionStore struct {
	instances []platform.ConnectionInstance
	getResult *platform.ConnectionInstance
	setErr    error
	deleteErr error
	listErr   error
	getErr    error
	// setCalls records each ConnectionInstance passed to Set so tests
	// can assert the persisted config is what the handler sent. Used
	// to prove fields like static_headers reach the store, not just
	// that the HTTP request returned 200.
	setCalls []platform.ConnectionInstance
}

func (m *mockConnectionStore) List(_ context.Context) ([]platform.ConnectionInstance, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.instances, nil
}

func (m *mockConnectionStore) Get(_ context.Context, _, _ string) (*platform.ConnectionInstance, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getResult, nil
}

func (m *mockConnectionStore) Set(_ context.Context, inst platform.ConnectionInstance) error {
	m.setCalls = append(m.setCalls, inst)
	return m.setErr
}

func (m *mockConnectionStore) Delete(_ context.Context, _, _ string) error {
	return m.deleteErr
}

// Verify interface compliance.
var _ ConnectionStore = (*mockConnectionStore)(nil)

// connTestHandler builds a Handler with the given connection store and mutable config store.
func connTestHandler(connStore ConnectionStore, mutable bool) *Handler {
	mode := "file"
	if mutable {
		mode = "database"
	}
	return NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: mode},
	}, nil)
}

// connTestHandlerWithCatalogStore mirrors connTestHandler but also
// wires an APICatalogStore so the api-kind catalog_id validator
// runs. Tests that exercise the api-kind path use this to confirm
// the validator rejects a missing catalog_id.
func connTestHandlerWithCatalogStore(connStore ConnectionStore, catStore APICatalogStore) *Handler {
	return NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
		APICatalogStore: catStore,
	}, nil)
}

// --- List ---

func TestListConnectionInstances(t *testing.T) {
	t.Run("success with entries", func(t *testing.T) {
		store := &mockConnectionStore{
			instances: []platform.ConnectionInstance{
				{Kind: "trino", Name: "prod", Description: "Production Trino", Config: map[string]any{"host": "trino.local"}},
				{Kind: "datahub", Name: "primary", Description: "Primary DataHub", Config: map[string]any{"url": "https://dh.local"}},
			},
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body, 2)
		assert.Equal(t, "trino", body[0].Kind)
		assert.Equal(t, "datahub", body[1].Kind)
	})

	t.Run("empty list returns empty array", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body, 0)
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{listErr: errors.New("db down")}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- Get ---

func TestGetConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{
			getResult: &platform.ConnectionInstance{
				Kind:        "trino",
				Name:        "prod",
				Description: "Production Trino",
				Config:      map[string]any{"host": "trino.local"},
				CreatedBy:   "admin@test.com",
			},
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "trino", body.Kind)
		assert.Equal(t, "prod", body.Name)
		assert.Equal(t, "trino.local", body.Config["host"])
	})

	t.Run("not found", func(t *testing.T) {
		store := &mockConnectionStore{
			getErr: platform.ErrConnectionNotFound,
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "not found")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{
			getErr: errors.New("db down"),
		}
		h := connTestHandler(store, false)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- Set ---

func TestSetConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local","port":8080},"description":"New Trino"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Equal(t, "trino", result.Kind)
		assert.Equal(t, "prod", result.Name)
		assert.Equal(t, "New Trino", result.Description)
		assert.Equal(t, "trino.local", result.Config["host"])
	})

	t.Run("success with user context", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local"},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx := context.WithValue(req.Context(), adminUserKey, &User{Email: "admin@test.com"})
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.Equal(t, "admin@test.com", result.CreatedBy)
	})

	t.Run("invalid kind returns 400", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"config":{},"description":"Bad kind"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/unknown/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "unknown connection kind")
	})

	t.Run("invalid body returns 400", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader("not-json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "invalid request body")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{setErr: errors.New("db down")}
		h := connTestHandler(store, true)

		body := `{"config":{"host":"trino.local"},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("nil config gets default empty map", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		body := `{"description":"No config"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result platform.ConnectionInstance
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		assert.NotNil(t, result.Config)
		assert.Empty(t, result.Config)
	})

	t.Run("read-only mode returns 404 for PUT", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, false) // file mode = not mutable

		body := `{"config":{},"description":"Test"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/connection-instances/trino/prod", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		// In file mode, the PUT route is not registered so mux returns 405 or 404
		assert.True(t, w.Code == http.StatusMethodNotAllowed || w.Code == http.StatusNotFound,
			"expected 404 or 405 in read-only mode, got %d", w.Code)
	})
}

// --- Delete ---

func TestDeleteConnectionInstance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockConnectionStore{}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		store := &mockConnectionStore{
			deleteErr: platform.ErrConnectionNotFound,
		}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "not found")
	})

	t.Run("store error returns 500", func(t *testing.T) {
		store := &mockConnectionStore{
			deleteErr: errors.New("db down"),
		}
		h := connTestHandler(store, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/connection-instances/trino/prod", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- Effective Connections ---

func TestListEffectiveConnections(t *testing.T) {
	t.Run("file-only connections", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod", tools: []string{"trino_query"}},
			},
		}
		h := NewHandler(Deps{
			Config:          testConfig(),
			ToolkitRegistry: reg,
			ConfigStore:     &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body, 1)
		assert.Equal(t, "trino", body[0].Kind)
		assert.Equal(t, "file", body[0].Source)
	})

	t.Run("DB-only connections", func(t *testing.T) {
		store := &mockConnectionStore{
			instances: []platform.ConnectionInstance{
				{Kind: "s3", Name: "lake", Description: "Data Lake", Config: map[string]any{"bucket": "b"}},
			},
		}
		h := NewHandler(Deps{
			Config:          testConfig(),
			ConnectionStore: store,
			ConfigStore:     &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body, 1)
		assert.Equal(t, "database", body[0].Source)
		assert.Equal(t, "lake", body[0].Name)
	})

	t.Run("both sources merged", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			allResult: []mockToolkit{
				{kind: "trino", name: "prod", connection: "prod", tools: []string{"trino_query"}},
			},
		}
		store := &mockConnectionStore{
			instances: []platform.ConnectionInstance{
				{Kind: "trino", Name: "prod", Description: "DB override", Config: map[string]any{"host": "trino.local"}},
				{Kind: "s3", Name: "lake", Description: "Data Lake", Config: map[string]any{"bucket": "b"}},
			},
		}
		h := NewHandler(Deps{
			Config:          testConfig(),
			ToolkitRegistry: reg,
			ConnectionStore: store,
			ConfigStore:     &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body, 2)

		// First: trino/prod from "both" sources
		assert.Equal(t, "both", body[0].Source)
		assert.Equal(t, "prod", body[0].Name)
		assert.Equal(t, "DB override", body[0].Description)

		// Second: s3/lake from database only
		assert.Equal(t, "database", body[1].Source)
		assert.Equal(t, "lake", body[1].Name)
	})

	t.Run("empty state returns empty array", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Len(t, body, 0)
	})

	t.Run("multi-connection toolkit expands all connections", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			rawToolkits: []registry.Toolkit{
				mockMultiConnectionToolkit{
					mockToolkit: mockToolkit{
						kind: "trino", name: "cassandra", connection: "cassandra",
						tools: []string{"trino_query", "trino_describe"},
					},
					connections: []toolkit.ConnectionDetail{
						{Name: "cassandra", Description: "Cassandra backend", IsDefault: true},
						{Name: "elasticsearch", Description: "Elasticsearch backend"},
						{Name: "warehouse", Description: "ERP data"},
					},
				},
			},
		}
		h := NewHandler(Deps{
			Config:          testConfig(),
			ToolkitRegistry: reg,
			ConfigStore:     &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"cassandra":     map[string]any{"host": "trino.example.com", "catalog": "cassandra"},
						"elasticsearch": map[string]any{"host": "trino.example.com", "catalog": "elasticsearch"},
						"warehouse":     map[string]any{"host": "trino.example.com", "catalog": "warehouse"},
					},
				},
			},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body, 3, "all three connections should be expanded")

		assert.Equal(t, "cassandra", body[0].Name)
		assert.Equal(t, "Cassandra backend", body[0].Description)
		assert.Equal(t, "trino.example.com", body[0].Config["host"])
		assert.Equal(t, "file", body[0].Source)

		assert.Equal(t, "elasticsearch", body[1].Name)
		assert.Equal(t, "Elasticsearch backend", body[1].Description)
		assert.Equal(t, "trino.example.com", body[1].Config["host"])

		assert.Equal(t, "warehouse", body[2].Name)
		assert.Equal(t, "ERP data", body[2].Description)
		assert.Equal(t, "trino.example.com", body[2].Config["host"])

		// All should share the same tools
		for _, ec := range body {
			assert.Equal(t, []string{"trino_query", "trino_describe"}, ec.Tools)
		}
	})

	t.Run("multi-connection with DB merge", func(t *testing.T) {
		reg := &mockToolkitRegistry{
			rawToolkits: []registry.Toolkit{
				mockMultiConnectionToolkit{
					mockToolkit: mockToolkit{
						kind: "trino", name: "cassandra", connection: "cassandra",
						tools: []string{"trino_query"},
					},
					connections: []toolkit.ConnectionDetail{
						{Name: "cassandra", Description: "File Cassandra", IsDefault: true},
						{Name: "elasticsearch", Description: "File ES"},
					},
				},
			},
		}
		store := &mockConnectionStore{
			instances: []platform.ConnectionInstance{
				{
					Kind: "trino", Name: "elasticsearch", Description: "DB override for ES",
					Config: map[string]any{"host": "es-override.example.com"},
				},
			},
		}
		h := NewHandler(Deps{
			Config:          testConfig(),
			ToolkitRegistry: reg,
			ConnectionStore: store,
			ConfigStore:     &mockConfigStore{mode: "database"},
		}, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-instances/effective", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var body []effectiveConnection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		require.Len(t, body, 2)

		assert.Equal(t, "cassandra", body[0].Name)
		assert.Equal(t, "file", body[0].Source)
		assert.Equal(t, "File Cassandra", body[0].Description, "file-only keeps toolkit description")

		assert.Equal(t, "elasticsearch", body[1].Name)
		assert.Equal(t, "both", body[1].Source)
		assert.Equal(t, "DB override for ES", body[1].Description, "DB description overrides file")
	})
}

func TestMergeConnections(t *testing.T) {
	t.Run("file and DB overlap uses both source", func(t *testing.T) {
		live := []liveConnectionInfo{
			{kind: "trino", name: "prod", connection: "prod", tools: []string{"trino_query"}},
		}
		db := []platform.ConnectionInstance{
			{Kind: "trino", Name: "prod", Description: "DB", Config: map[string]any{"host": "h"}},
		}

		result := mergeConnections(live, db)
		require.Len(t, result, 1)
		assert.Equal(t, "both", result[0].Source)
		assert.Equal(t, "DB", result[0].Description)
		assert.Equal(t, []string{"trino_query"}, result[0].Tools)
	})

	t.Run("DB-only entries appended", func(t *testing.T) {
		live := []liveConnectionInfo{
			{kind: "trino", name: "prod", connection: "prod", tools: []string{"trino_query"}},
		}
		db := []platform.ConnectionInstance{
			{Kind: "s3", Name: "lake", Description: "Lake"},
		}

		result := mergeConnections(live, db)
		require.Len(t, result, 2)
		assert.Equal(t, "file", result[0].Source)
		assert.Equal(t, "database", result[1].Source)
	})

	t.Run("nil inputs return empty array", func(t *testing.T) {
		result := mergeConnections(nil, nil)
		assert.NotNil(t, result)
		assert.Len(t, result, 0)
	})

	t.Run("nil Tools from live connection normalizes to empty slice", func(t *testing.T) {
		live := []liveConnectionInfo{
			{kind: "mcp", name: "gateway", connection: "", tools: nil},
		}
		result := mergeConnections(live, nil)
		require.Len(t, result, 1)
		assert.NotNil(t, result[0].Tools, "Tools must not be nil after merge")
		assert.Equal(t, []string{}, result[0].Tools)
	})

	t.Run("DB-only entries get empty Tools slice not nil", func(t *testing.T) {
		db := []platform.ConnectionInstance{
			{Kind: "trino", Name: "prod", Description: "DB"},
		}
		result := mergeConnections(nil, db)
		require.Len(t, result, 1)
		assert.NotNil(t, result[0].Tools, "DB-only Tools must not be nil")
		assert.Equal(t, []string{}, result[0].Tools)
	})
}

// --- Redaction ---

func TestRedactConnectionConfig(t *testing.T) {
	t.Run("redacts sensitive keys", func(t *testing.T) {
		config := map[string]any{
			"host":              "trino.local",
			"password":          "secret123",
			"secret_access_key": "AKIA...",
			"api_key":           "key123",
		}

		redacted := redactConnectionConfig(config)
		assert.Equal(t, "trino.local", redacted["host"])
		assert.Equal(t, "[REDACTED]", redacted["password"])
		assert.Equal(t, "[REDACTED]", redacted["secret_access_key"])
		assert.Equal(t, "[REDACTED]", redacted["api_key"])
	})

	t.Run("nil config returns nil", func(t *testing.T) {
		assert.Nil(t, redactConnectionConfig(nil))
	})

	t.Run("does not modify original", func(t *testing.T) {
		config := map[string]any{"password": "secret"}
		_ = redactConnectionConfig(config)
		assert.Equal(t, "secret", config["password"])
	})

	t.Run("removes platform-internal keys", func(t *testing.T) {
		config := map[string]any{
			"host":             "trino.local",
			"elicitation":      map[string]any{"enabled": true},
			"progress_enabled": true,
		}

		redacted := redactConnectionConfig(config)
		assert.Equal(t, "trino.local", redacted["host"])
		_, hasElicitation := redacted["elicitation"]
		assert.False(t, hasElicitation, "elicitation should be removed")
		_, hasProgress := redacted["progress_enabled"]
		assert.False(t, hasProgress, "progress_enabled should be removed")
	})
}

func TestMergeRedactedFields(t *testing.T) {
	submitted := map[string]any{
		"host":     "new-host",
		"password": "[REDACTED]",
		"api_key":  "new-key",
	}
	existing := map[string]any{
		"host":     "old-host",
		"password": "real-password",
		"api_key":  "old-key",
	}

	merged := mergeRedactedFields(submitted, existing)
	assert.Equal(t, "new-host", merged["host"])
	assert.Equal(t, "real-password", merged["password"])
	assert.Equal(t, "new-key", merged["api_key"])
}

func TestHasRedactedValues(t *testing.T) {
	assert.True(t, hasRedactedValues(map[string]any{"password": "[REDACTED]"}))
	assert.False(t, hasRedactedValues(map[string]any{"password": "real"}))
	assert.False(t, hasRedactedValues(map[string]any{"host": "[REDACTED]"}))
	assert.False(t, hasRedactedValues(map[string]any{}))
}

func TestRedactConnectionConfig_StaticHeaders(t *testing.T) {
	t.Run("redacts inner values, keeps names", func(t *testing.T) {
		config := map[string]any{
			"static_headers": map[string]any{
				"X-Goog-User-Project": "real-secret",
				"X-Tag":               "ops",
			},
		}
		redacted := redactConnectionConfig(config)
		inner, ok := redacted["static_headers"].(map[string]any)
		require.True(t, ok, "static_headers must remain a map post-redaction")
		assert.Equal(t, "[REDACTED]", inner["X-Goog-User-Project"])
		assert.Equal(t, "[REDACTED]", inner["X-Tag"])
	})

	t.Run("accepts map[string]string from in-memory configs", func(t *testing.T) {
		config := map[string]any{
			"static_headers": map[string]string{"X-Subscription": "sub-key"},
		}
		redacted := redactConnectionConfig(config)
		inner, ok := redacted["static_headers"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "[REDACTED]", inner["X-Subscription"])
	})
}

// TestRedactConnectionConfig_MTLSExpirySurfaced verifies that GET
// responses include the leaf certificate's NotAfter as
// mtls_cert_not_after (RFC3339, UTC) so the portal can render an
// expiry badge without re-parsing the PEM client-side. The
// expectation: when a connection carries a valid mtls_client_cert_pem,
// the redacted output gains the expiry field; without a cert, no
// such field appears.
func TestRedactConnectionConfig_MTLSExpirySurfaced(t *testing.T) {
	certPEM := mintTestLeafForRedactionTest(t, time.Now().Add(60*24*time.Hour))
	got := redactConnectionConfig(map[string]any{
		"mtls_client_cert_pem": certPEM,
		"mtls_client_key_pem":  "anything-here-because-it-gets-redacted",
	})
	assert.Equal(t, "[REDACTED]", got["mtls_client_key_pem"], "private key must be redacted")
	raw, ok := got["mtls_cert_not_after"].(string)
	require.True(t, ok, "expiry field must be added as a string")
	parsed, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	assert.True(t, parsed.After(time.Now()))
	// No cert: no expiry field.
	noCert := redactConnectionConfig(map[string]any{"base_url": "https://x"})
	_, present := noCert["mtls_cert_not_after"]
	assert.False(t, present, "no expiry field without a cert")
	// Garbage cert: no expiry field (zero time is filtered).
	garbage := redactConnectionConfig(map[string]any{"mtls_client_cert_pem": "not pem"})
	_, present = garbage["mtls_cert_not_after"]
	assert.False(t, present, "unparseable cert must not surface a zero-time expiry")
}

// TestRedactConnectionConfig_MTLSExpiryIsServerDerivedNotPersisted is
// the regression guard for the PUT round-trip bug. The UI loads
// mtls_cert_not_after from a GET response, includes it in the next
// PUT body, and the server must NOT persist it: the field is a
// server-derived view of the leaf cert, not operator config. The
// two checks below capture the two failure modes the bug had:
//
//  1. setConnectionInstance is expected to strip the field from
//     req.Config before persistence, but the unit-level invariant
//     we can assert here is that redactConnectionConfig filters any
//     stale value from the stored config on GET. A stale value
//     (left over from a pre-fix deployment that persisted the field
//     via PUT) plus a removed cert would otherwise let the portal
//     falsely report the connection still had a valid cert.
//  2. With no cert configured, no expiry field appears in the
//     response even if the underlying config map carries a stale
//     value.
func TestRedactConnectionConfig_MTLSExpiryIsServerDerivedNotPersisted(t *testing.T) {
	// (1) Stale persisted value + cert removed: response must omit
	// the expiry field rather than echoing the stale string.
	stale := redactConnectionConfig(map[string]any{
		"base_url":            "https://x",
		"mtls_cert_not_after": "2020-01-01T00:00:00Z",
	})
	_, present := stale["mtls_cert_not_after"]
	assert.False(t, present, "stale persisted expiry must be filtered when no cert is present")

	// (2) Stale persisted value + fresh cert: response must carry
	// the freshly computed value, not the stale one.
	freshCert := mintTestLeafForRedactionTest(t, time.Now().Add(120*24*time.Hour))
	mixed := redactConnectionConfig(map[string]any{
		"mtls_client_cert_pem": freshCert,
		"mtls_cert_not_after":  "2020-01-01T00:00:00Z",
	})
	raw, ok := mixed["mtls_cert_not_after"].(string)
	require.True(t, ok, "fresh cert must repopulate the expiry field")
	got, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	assert.True(t, got.After(time.Now().Add(60*24*time.Hour)),
		"recomputed expiry must reflect the actual leaf NotAfter, not the stale stored value")
}

// TestSetConnectionInstance_StripsMTLSCertNotAfterFromIncomingBody
// proves the second half of the fix end-to-end: a PUT that includes
// mtls_cert_not_after (the shape the UI sends when it loads GET into
// the editor and re-saves) does NOT round-trip the field into the
// connection store. The check is on the persisted ConnectionInstance,
// not the HTTP response, because the response goes through
// redactConnectionConfig which would always strip the field; only
// reaching into the store proves the strip happened before persist.
func TestSetConnectionInstance_StripsMTLSCertNotAfterFromIncomingBody(t *testing.T) {
	store := &mockConnectionStore{}
	h := connTestHandler(store, true)
	body := strings.NewReader(`{
		"config": {
			"base_url": "https://upstream.example",
			"mtls_cert_not_after": "2020-01-01T00:00:00Z"
		},
		"description": "test"
	}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/trino/test", body)
	req.SetPathValue("kind", "trino")
	req.SetPathValue("name", "test")
	rr := httptest.NewRecorder()
	h.setConnectionInstance(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "unexpected: %s", rr.Body.String())

	require.Len(t, store.setCalls, 1, "PUT must invoke ConnectionStore.Set exactly once")
	_, present := store.setCalls[0].Config["mtls_cert_not_after"]
	assert.False(t, present, "server-derived field must be stripped before persistence")
}

// mintTestLeafForRedactionTest creates a throwaway self-signed cert
// for the redaction test. Stays inside the test file so it never
// reaches a code path outside the admin package's test boundary.
func mintTestLeafForRedactionTest(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "redaction-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestHasRedactedValues_StaticHeaders(t *testing.T) {
	assert.True(t, hasRedactedValues(map[string]any{
		"static_headers": map[string]any{"X-Sub": "[REDACTED]"},
	}))
	assert.False(t, hasRedactedValues(map[string]any{
		"static_headers": map[string]any{"X-Sub": "real-value"},
	}))
}

func TestHasRedactedValues_StaticHeaders_MapStringString(t *testing.T) {
	assert.True(t, hasRedactedValues(map[string]any{
		"static_headers": map[string]string{"X-Sub": "[REDACTED]"},
	}))
	assert.False(t, hasRedactedValues(map[string]any{
		"static_headers": map[string]string{"X-Sub": "real"},
	}))
}

func TestNestedMapHasRedacted_NonMapPassthrough(t *testing.T) {
	assert.False(t, nestedMapHasRedacted(nil))
	assert.False(t, nestedMapHasRedacted("not-a-map"))
	assert.False(t, nestedMapHasRedacted(42))
}

func TestNestedMapAsAny_AllShapes(t *testing.T) {
	t.Run("map[string]any returned as-is", func(t *testing.T) {
		in := map[string]any{"k": "v"}
		got := nestedMapAsAny(in)
		require.NotNil(t, got)
		assert.Equal(t, "v", got["k"])
	})
	t.Run("map[string]string upcast", func(t *testing.T) {
		got := nestedMapAsAny(map[string]string{"a": "b"})
		require.NotNil(t, got)
		assert.Equal(t, "b", got["a"])
	})
	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, nestedMapAsAny(nil))
	})
	t.Run("non-map returns nil", func(t *testing.T) {
		assert.Nil(t, nestedMapAsAny("oops"))
	})
}

func TestMergeRedactedFields_StaticHeaders(t *testing.T) {
	t.Run("redacted inner values are restored from existing", func(t *testing.T) {
		submitted := map[string]any{
			"static_headers": map[string]any{
				"X-Goog-User-Project": "[REDACTED]",
				"X-New":               "freshly-added",
			},
		}
		existing := map[string]any{
			"static_headers": map[string]any{
				"X-Goog-User-Project": "real-secret",
				"X-Removed":           "stale-value",
			},
		}
		merged := mergeRedactedFields(submitted, existing)
		inner, ok := merged["static_headers"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "real-secret", inner["X-Goog-User-Project"])
		assert.Equal(t, "freshly-added", inner["X-New"])
		_, hasRemoved := inner["X-Removed"]
		assert.False(t, hasRemoved, "operator-deleted header must not be resurrected")
	})

	t.Run("absent submitted leaves field absent", func(t *testing.T) {
		submitted := map[string]any{"base_url": "https://x"}
		existing := map[string]any{"static_headers": map[string]any{"X": "y"}}
		merged := mergeRedactedFields(submitted, existing)
		_, has := merged["static_headers"]
		assert.False(t, has, "absent static_headers must not be revived from existing")
	})
}

func TestLookupToolkitInstanceConfig(t *testing.T) {
	t.Run("returns config for existing instance", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"prod": map[string]any{
							"host": "trino.example.com",
							"port": 443,
						},
					},
				},
			},
		}, nil)

		cfg := h.lookupToolkitInstanceConfig("trino", "prod")
		require.NotNil(t, cfg)
		assert.Equal(t, "trino.example.com", cfg["host"])
		assert.Equal(t, 443, cfg["port"])
	})

	t.Run("returns nil for missing kind", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{},
			},
		}, nil)

		assert.Nil(t, h.lookupToolkitInstanceConfig("s3", "lake"))
	})

	t.Run("returns nil for missing instance", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"prod": map[string]any{"host": "trino.local"},
					},
				},
			},
		}, nil)

		assert.Nil(t, h.lookupToolkitInstanceConfig("trino", "staging"))
	})

	t.Run("returns nil for nil config", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
		}, nil)

		assert.Nil(t, h.lookupToolkitInstanceConfig("trino", "prod"))
	})

	t.Run("returns nil for missing instances key", func(t *testing.T) {
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{"enabled": true},
			},
		}, nil)

		assert.Nil(t, h.lookupToolkitInstanceConfig("trino", "prod"))
	})

	t.Run("returns a copy not a reference", func(t *testing.T) {
		original := map[string]any{"host": "trino.local", "password": "secret"}
		h := NewHandler(Deps{
			Config:      testConfig(),
			ConfigStore: &mockConfigStore{mode: "database"},
			ToolkitsConfig: map[string]any{
				"trino": map[string]any{
					"instances": map[string]any{
						"prod": original,
					},
				},
			},
		}, nil)

		cfg := h.lookupToolkitInstanceConfig("trino", "prod")
		require.NotNil(t, cfg)

		// Mutate the returned copy
		cfg["host"] = "MUTATED"

		// Original should be unchanged
		assert.Equal(t, "trino.local", original["host"])
	})
}

func TestSetConnectionInstance_APICatalogValidation(t *testing.T) {
	t.Run("rejects unknown catalog_id", func(t *testing.T) {
		store := &mockConnectionStore{}
		catStore := apicatalog.NewMemoryStore()
		h := connTestHandlerWithCatalogStore(store, catStore)

		body := `{"config":{"base_url":"https://x","catalog_id":"ghost"},"description":""}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/connection-instances/api/c", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "ghost")
	})

	t.Run("accepts existing catalog_id", func(t *testing.T) {
		store := &mockConnectionStore{}
		catStore := apicatalog.NewMemoryStore()
		_ = catStore.CreateCatalog(context.Background(), apicatalog.Catalog{
			ID: "petstore", Name: "petstore", DisplayName: "Petstore",
		})
		h := connTestHandlerWithCatalogStore(store, catStore)

		body := `{"config":{"base_url":"https://x","catalog_id":"petstore"},"description":""}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/connection-instances/api/c", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}
