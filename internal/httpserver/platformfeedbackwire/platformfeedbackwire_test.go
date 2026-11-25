package platformfeedbackwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platformfeedback"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// TestSealedValues: the values a post is checked against are those under the
// keys the encryption layer seals, nested maps' values included, and nothing
// else in a connection's configuration.
func TestSealedValues(t *testing.T) {
	sealed := fieldcrypt.SensitiveConfigKeyList()[0]
	nested := fieldcrypt.SensitiveNestedMapKeyList()[0]
	got := sealedValues(map[string]any{
		sealed: "s3cret-value", "host": "db.internal", "timeout": 30,
		nested: map[string]any{"Authorization": "Bearer tok-1", "empty": ""},
	})
	slices.Sort(got)
	assert.Equal(t, []string{"Bearer tok-1", "s3cret-value"}, got)
	assert.Equal(t, []string{"tok-2"}, sealedValues(map[string]any{nested: map[string]string{"X-Key": "tok-2", "none": ""}}))
	assert.Empty(t, sealedValues(map[string]any{"host": "db.internal"}))
}

// TestWiringWithoutADatabase: no database is no service, and binding or
// mounting nothing does nothing; a service mounts its routes behind the admin
// wrap.
func TestWiringWithoutADatabase(t *testing.T) {
	p := &platform.Platform{}
	assert.Nil(t, Service(p, nil))
	Bind(p, nil)
	mux := http.NewServeMux()
	Mount(mux, nil, func(h http.Handler) http.Handler { return h }, nil)
	Mount(mux, platformfeedback.New(platformfeedback.Deps{}), nil, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/settings/platform-feedback", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "nothing mounted without a service and the admin API")

	wrapped := false
	Mount(mux, platformfeedback.New(platformfeedback.Deps{}), func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wrapped = true; h.ServeHTTP(w, r) })
	}, func(*http.Request) string { return "admin@example.com" })
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/settings/platform-feedback", http.NoBody))
	assert.True(t, wrapped, "the routes are served behind the admin wrap")
	assert.Equal(t, http.StatusOK, rec.Code)
}
