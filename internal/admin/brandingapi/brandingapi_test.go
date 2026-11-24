package brandingapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/refscheme"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestHandlerReportsTheReferenceScheme(t *testing.T) {
	require.NoError(t, refscheme.Configure("acme", ""))
	t.Cleanup(func() { _ = refscheme.Configure("", "") })
	cfg := &platform.Config{}
	cfg.Portal.Title = "ACME"
	rec := httptest.NewRecorder()
	Handler(cfg)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/public/branding", http.NoBody))
	var body Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "acme", body.ReferenceScheme)
	assert.Equal(t, "ACME", body.PortalTitle)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	rec = httptest.NewRecorder()
	Handler(nil)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/public/branding", http.NoBody))
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.AllowAPIKeyLogin)
}
