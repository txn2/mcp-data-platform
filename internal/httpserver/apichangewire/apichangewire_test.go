package apichangewire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/admin/apichangeapi"
	"github.com/txn2/mcp-data-platform/internal/apichange"
	"github.com/txn2/mcp-data-platform/internal/upstreamwarn"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

func TestBuild_NoCatalogStoreIsNil(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "apichangewire-test", Transport: "http"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	f := Build(p, nil)
	assert.Nil(t, f)
	assert.Nil(t, Build(nil, nil))
	// A nil feature does nothing.
	f.Mount(http.NewServeMux(), p, nil, nil)
	f.Start(context.Background())
	f.Stop()
}

// With a database the feature keeps watches and reports, records warnings
// from the outbound chain, and serves every route.
func TestWithDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { upstreamwarn.SetObserver(nil) })
	mock.MatchExpectationsInOrder(false)

	store := apicatalog.NewMemoryStore()
	r := &apichange.Refresher{Catalogs: store}
	f := &Feature{refresher: r, routes: apichangeapi.Config{Service: r, Catalogs: store}}
	f.withDatabase(db, "https://portal.example", nil)
	require.NotNil(t, f.loop)
	assert.NotNil(t, f.refresher.Store)
	assert.NotNil(t, f.routes.Store)
	assert.NotNil(t, f.routes.Warnings)
	assert.Nil(t, f.routes.ChannelExists, "no notification handle, no channel check")

	ctx := context.Background()
	mock.ExpectQuery("FROM api_operation_warnings").WillReturnRows(sqlmock.NewRows([]string{"connection"}))
	_, _ = f.routes.Warnings(ctx, "vendor")
	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}))
	got, err := f.routes.ReadSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got.Channels)
	mock.ExpectExec("platform_settings").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, f.routes.SaveSettings(ctx, apichange.Settings{Channels: []string{}}, "a"))

	mux := http.NewServeMux()
	apichangeapi.Register(mux, func(h http.Handler) http.Handler { return h }, f.routes)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/api-catalogs/acme/specs/pets/watch", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "the watch route is mounted and answers for a missing spec")
}

func TestInstallPrefixes(t *testing.T) {
	t.Cleanup(func() { apicatalog.SetAllowPrivateCIDRs(nil) })
	installPrefixes(apicatalog.FetchConfig{})
	installPrefixes(apicatalog.FetchConfig{AllowPrivateCIDRs: []string{"not-a-cidr"}})
	installPrefixes(apicatalog.FetchConfig{AllowPrivateCIDRs: []string{"10.0.0.0/8"}})
}

type catalogToolkit struct {
	registry.Toolkit
	catalog string
}

func (c catalogToolkit) CatalogOf(context.Context, string) string { return c.catalog }

func TestCatalogOf(t *testing.T) {
	assert.Empty(t, catalogOf(nil)(context.Background(), "vendor"))
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(catalogToolkit{Toolkit: apigatewaykit.New("empty"), catalog: ""}))
	assert.Empty(t, catalogOf(reg)(context.Background(), "vendor"))
}

// A platform with an API catalog store and no database builds the refresh
// and its routes, and keeps no state: no watches, no schedule.
func TestBuild_WithACatalogStore(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "apichangewire-test", Transport: "http"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	mc, err := apigatewaykit.ParseMultiConfig("api", map[string]map[string]any{
		"vendor": {"base_url": "https://vendor.example", "catalog_id": "acme"},
	})
	require.NoError(t, err)
	tk := apigatewaykit.NewMulti(mc)
	t.Cleanup(func() { _ = tk.Close() })
	require.NoError(t, p.ToolkitRegistry().Register(tk))
	store := apicatalog.NewMemoryStore()
	require.NoError(t, store.CreateCatalog(context.Background(), apicatalog.Catalog{ID: "acme", Name: "acme", DisplayName: "Acme"}))
	p.WireAPIGatewayCatalogStore(store)

	f := Build(p, nil)
	require.NotNil(t, f)
	assert.Nil(t, f.refresher.Store, "no database: no reports kept")
	assert.Equal(t, "acme", f.routes.CatalogOf(context.Background(), "vendor"))

	mux := http.NewServeMux()
	f.Mount(mux, p, func(*platform.Platform) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler { return h }
	}, func(*http.Request) string { return "admin@example.com" })
	f.Start(context.Background())
	f.Stop()
}
