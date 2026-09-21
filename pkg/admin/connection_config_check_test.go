package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putConnectionConfig issues a connection write from a config map and returns
// the recorder. It is putConnection's sibling: that one takes a raw body, which
// is what the OAuth-vocabulary cases need; these cases are about values inside
// a config, so they compose the object.
func putConnectionConfig(t *testing.T, h *Handler, kind, name string, config map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"config": config})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/"+kind+"/"+name, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A database-managed connection is stored with its values exactly as they
// arrive: only the platform's configuration FILE expands ${VAR}. A placeholder
// here used to be stored, listed and reported as created, and then failed every
// call several layers away at DSN construction (#1805).
func TestSetConnectionInstance_RefusesUnexpandedPlaceholder(t *testing.T) {
	store := &mockConnectionStore{}
	rec := putConnectionConfig(t, connTestHandler(store, true), "trino", "warehouse", map[string]any{
		"host":     "trino.example.com",
		"user":     "${TRINO_USER}",
		"password": "${TRINO_PASSWORD}",
	})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	// The refusal names the offending keys, because a config carrying several
	// placeholders should take one round trip to fix, not one per key.
	assert.Contains(t, rec.Body.String(), "user")
	assert.Contains(t, rec.Body.String(), "password")
	assert.Contains(t, rec.Body.String(), "configuration file")
	assert.Empty(t, store.setCalls, "a refused config must not reach the store")
}

// A placeholder nested inside an object is the same defect: a connection's
// config is not flat, and an api connection's static headers are where a
// credential-shaped value most often sits.
func TestSetConnectionInstance_RefusesNestedPlaceholder(t *testing.T) {
	store := &mockConnectionStore{}
	rec := putConnectionConfig(t, connTestHandler(store, true), "api", "vendor", map[string]any{
		"base_url":       "https://api.example.com",
		"static_headers": map[string]any{"X-Subscription": "${VENDOR_KEY}"},
	})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "static_headers.X-Subscription")
	assert.Empty(t, store.setCalls)
}

// The refusal is about a placeholder standing unexpanded, not about the
// characters: a config with no "${" is stored as it always was.
func TestSetConnectionInstance_AcceptsResolvedValues(t *testing.T) {
	store := &mockConnectionStore{}
	rec := putConnectionConfig(t, connTestHandler(store, true), "trino", "warehouse", map[string]any{
		"host": "trino.example.com", "user": "analyst", "password": "s3cret",
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, store.setCalls, 1)
	assert.Equal(t, "analyst", store.setCalls[0].Config["user"])
}

// checkNoPlaceholders walks slices as well as maps, so one entry of a list is
// named rather than the list.
func TestCheckNoPlaceholders_NamesTheOffendingPath(t *testing.T) {
	err := checkNoPlaceholders(map[string]any{
		"hosts": []any{"a.example.com", "${FALLBACK_HOST}"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hosts[1]")
}

// A config with nothing to report returns no error, including one holding
// values that merely contain a dollar sign.
func TestCheckNoPlaceholders_Clean(t *testing.T) {
	require.NoError(t, checkNoPlaceholders(map[string]any{
		"host": "trino.example.com", "password": "a$dollar$sign", "port": 443, "ssl": true,
	}))
}

// A value that merely contains a dollar sign is not a placeholder, and a
// password legitimately containing "${" would be refused — which is the one
// false positive this check can produce. It is preferred to the alternative:
// a stored placeholder is unusable, while such a password can be changed.
func TestCheckNoPlaceholders_DollarSignAlone(t *testing.T) {
	if err := checkNoPlaceholders(map[string]any{"password": "pa$$word"}); err != nil {
		t.Errorf("a password with dollar signs was refused: %v", err)
	}
	if err := checkNoPlaceholders(map[string]any{"password": "literal ${BRACE} inside"}); err == nil {
		t.Error("a value carrying ${...} was accepted")
	}
}
