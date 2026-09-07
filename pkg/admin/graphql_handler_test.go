package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// graphQLToolkit builds a toolkit holding one connection whose endpoint
// is never dialed: what these tests exercise is the mount, not the read.
func graphQLToolkit(t *testing.T) *graphqlkit.Toolkit {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": "https://unreached.invalid/graphql"})
	require.NoError(t, err)
	cfg.ConnectionName = "erp"
	return graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "erp", Instances: map[string]graphqlkit.Config{"erp": cfg},
	})
}

func TestGraphQLSchemaRouteIsMounted(t *testing.T) {
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{graphQLToolkit(t)}},
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connection-instances/graphql/erp/schema", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "the schema route was not mounted")
	assert.Contains(t, w.Body.String(), `"connection":"erp"`)
}

func TestGraphQLRoutesSkipWithoutAToolkitRegistry(t *testing.T) {
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connection-instances/graphql/erp/schema", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// The routes mount, but no toolkit holds the connection.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGraphQLRefreshRouteIsWithheldInFileMode(t *testing.T) {
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{graphQLToolkit(t)}},
		ConfigStore:     &mockConfigStore{mode: "file"},
	}, nil)

	// A refresh writes the schema to the store, and a file-configured
	// deployment has nowhere to put it.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/erp/refresh-schema", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
