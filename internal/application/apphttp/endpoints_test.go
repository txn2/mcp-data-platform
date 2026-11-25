package apphttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epaddr"
)

// TestDetailCarriesEachEnvironmentsEndpoints is #2822: an application's page
// says what each environment serves, every environment present, from the
// source configured, and with none when there is none.
func TestDetailCarriesEachEnvironmentsEndpoints(t *testing.T) {
	shop := &application.Application{ID: "app_1", Name: "shop", Environments: []application.Environment{{Name: "dev"}, {Name: "prod"}}}
	read := func(cfg Config) map[string][]epaddr.Endpoint {
		t.Helper()
		cfg.Service = &appsvc.Service{Store: &store{apps: map[string]*application.Application{"shop": shop}}, Scripts: scripts{}}
		cfg.Actor = func(*http.Request) *application.Actor { return &application.Actor{Email: "jane@example.com"} }
		mux := http.NewServeMux()
		Register(mux, func(h http.Handler) http.Handler { return h }, cfg)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, Prefix+"/shop", http.NoBody))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var got struct {
			Endpoints map[string][]epaddr.Endpoint `json:"endpoints"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		return got.Endpoints
	}

	assert.Equal(t, map[string][]epaddr.Endpoint{"dev": {}, "prod": {}}, read(Config{}), "no source: every environment, with none")

	want := map[string][]epaddr.Endpoint{"dev": {}, "prod": {{Name: "research", URLs: []string{"https://p.example.com/endpoints/research"}}}}
	got := read(Config{Endpoints: func(_ context.Context, a *application.Application) map[string][]epaddr.Endpoint {
		assert.Equal(t, "app_1", a.ID)
		return want
	}})
	assert.Equal(t, want["prod"][0].URLs, got["prod"][0].URLs)
}
