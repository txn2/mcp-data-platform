package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// TestSetConnectionInstance_RefusesWhatTheClientRefuses is #2014's admin half:
// a Trino connection its client will never open -- here one inheriting the
// default connection's password over plain HTTP -- is refused at the save with
// the reason and the fix, and nothing is stored.
func TestSetConnectionInstance_RefusesWhatTheClientRefuses(t *testing.T) {
	yes := true
	tk, err := trinokit.NewMulti(trinokit.MultiConfig{Instances: map[string]trinokit.Config{
		"main": {Host: "trino.example.com", User: "svc", Password: "p", SSL: &yes},
	}})
	require.NoError(t, err)
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
	}, nil)

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
			"/api/v1/admin/connection-instances/trino/reporting", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	refused := put(`{"config":{"host":"trino.internal","port":8080,"ssl":false}}`)
	assert.Equal(t, http.StatusBadRequest, refused.Code)
	assert.Contains(t, refused.Body.String(), "set ssl: true")
	assert.Empty(t, store.setCalls, "a refused connection is not stored")

	accepted := put(`{"config":{"host":"trino2.example.com","ssl":true}}`)
	assert.Equal(t, http.StatusOK, accepted.Code)
	assert.Len(t, store.setCalls, 1)
}
