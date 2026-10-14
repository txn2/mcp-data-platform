package catalogwire

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/apigwwiring"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

func graphqlToolkit(t *testing.T) *graphqlkit.Toolkit {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": "https://unreached.invalid/graphql"})
	require.NoError(t, err)
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "gh", Instances: map[string]graphqlkit.Config{"gh": cfg},
	})
	sdl, err := os.ReadFile("../../../internal/gqlschema/testdata/namespaced.graphql")
	require.NoError(t, err)
	require.NoError(t, tk.SetSchema(context.Background(), "gh", sdl))
	return tk
}

func TestGraphQLReader(t *testing.T) {
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(graphqlToolkit(t)))
	r := graphqlReader{reg: reg}

	conns := r.Connections()
	require.Len(t, conns, 1)
	assert.Equal(t, "gh", conns[0].Instance)

	ops, err := r.Operations(context.Background(), "gh")
	require.NoError(t, err)
	require.NotEmpty(t, ops)
	for _, op := range ops {
		assert.Contains(t, []string{"query", "mutation"}, op.Path[0], "every operation is named under its kind")
	}

	_, err = r.Operations(context.Background(), "nope")
	assert.Error(t, err)

	// Every operation is read from the connection's one SDL document.
	assert.Equal(t, graphqlSchemaKey, ops[0].Document)
	docs, err := r.Documents(context.Background(), "gh")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "schema.graphql", docs[0].Filename)
	assert.Contains(t, string(docs[0].Content), "type RootQueryType")
	docs, err = r.Documents(context.Background(), "nope")
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestAPIOperationNames(t *testing.T) {
	ops := apiOperations([]apigateway.OperationSummary{
		{OperationID: "listCharges", Spec: "billing"},
		{OperationID: "get", Spec: "billing"},
		{OperationID: "get", Spec: "users"},
		{OperationID: ""},
	})
	require.Len(t, ops, 3, "an operation with no id is not cataloged")
	assert.Equal(t, []string{"listCharges"}, ops[0].Path)
	assert.Equal(t, []string{"billing", "get"}, ops[1].Path, "an id two specs share is named by its spec")
	assert.Equal(t, []string{"users", "get"}, ops[2].Path)
}

func TestAPIReaderUnknownConnection(t *testing.T) {
	r := apiReader{reg: registry.NewRegistry()}
	assert.Empty(t, r.Connections())
	_, err := r.Operations(context.Background(), "nope")
	assert.Error(t, err)
}

func TestTrinoReaderConnectionsAndClients(t *testing.T) {
	multi, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]trinokit.Config{"warehouse": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = multi.Close() })
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(multi))
	r := trinoReader{reg: reg}

	assert.Equal(t, []catalogscan.Connection{{Instance: "warehouse", Bound: "warehouse"}}, r.Connections())
	c, err := r.client("warehouse")
	require.NoError(t, err)
	assert.NotNil(t, c)
	_, err = r.client("nope")
	assert.Error(t, err)
	_, err = r.Catalogs(context.Background(), "nope")
	assert.Error(t, err)
	assert.Error(t, r.Columns(context.Background(), "nope", "hive", nil))
}

func TestHelpers(t *testing.T) {
	assert.Equal(t, `"we""ird"`, quoteIdent(`we"ird`))
	assert.Empty(t, str(nil))
	assert.Equal(t, "x", str("x"))
	assert.Equal(t, "3", str(3))
	assert.Empty(t, liveConnections(nil, "trino"))
}

// dblessPlatform is a platform with no database.
type dblessPlatform struct{}

func (dblessPlatform) DB() *sql.DB                         { return nil }
func (dblessPlatform) ToolkitRegistry() *registry.Registry { return nil }

func TestNoDatabaseBuildsNothing(t *testing.T) {
	assert.Nil(t, Build(nil))
	var c *Catalog
	c.Start(context.Background())
	c.Stop()
	c.MountAdmin(http.NewServeMux(), nil, nil)
	inner := RegistrationStore(dblessPlatform{}, nil)
	assert.Nil(t, inner, "with no database the store is returned as it was")
}

func TestBuildMountsAndRuns(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := assemble(db, registry.NewRegistry())
	require.NotNil(t, c.Settings)

	mux := http.NewServeMux()
	c.MountAdmin(mux, nil, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/catalog/settings", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "the routes stay off when the admin API is")

	c.admin = true
	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value"}))
	c.MountAdmin(mux, func(*platform.Platform) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler { return h }
	}, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/catalog/settings", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)

	// The loop's first check takes the lock; another replica holding it ends
	// the check, and Stop returns once the loop has.
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	ctx, cancel := context.WithCancel(context.Background())
	c.Start(ctx)
	cancel()
	c.Stop()
}

// The import runner reads the DataHub the configuration names, and its routes
// are mounted beside the scan's.
func TestImportRunnerAndRoutes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.Empty(t, importRunner(db, nil).Configured())

	c := assemble(db, registry.NewRegistry())
	c.admin = true
	c.Import = importRunner(db, &platform.DataHubImport{URL: "http://datahub:8080", Token: "t", Platform: "postgres"})
	assert.Equal(t, "http://datahub:8080", c.Import.Configured())
	mux := http.NewServeMux()
	c.MountAdmin(mux, func(*platform.Platform) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler { return h }
	}, nil)
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM catalog_imports").WillReturnRows(sqlmock.NewRows(
		[]string{"id", "source_url", "started_by", "started_at", "finished_at", "report", "error"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/catalog/imports", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"imports":[],"configured":"http://datahub:8080"}`, rec.Body.String())
	c.Stop()
}

func TestTrinoInstance(t *testing.T) {
	multi, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]trinokit.Config{"warehouse": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = multi.Close() })
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(multi))
	assert.Equal(t, "warehouse", trinoInstance(reg)("warehouse"))
	assert.Equal(t, "gone", trinoInstance(reg)("gone"))
	assert.Equal(t, "x", trinoInstance(nil)("x"))
}

// dbPlatform is a platform with a database.
type dbPlatform struct{ db *sql.DB }

func (d dbPlatform) DB() *sql.DB                       { return d.db }
func (dbPlatform) ToolkitRegistry() *registry.Registry { return registry.NewRegistry() }

func TestRegistrationStoreWrapsWithADatabase(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	got := RegistrationStore(dbPlatform{db: db}, nil)
	_, ok := got.(*tableregister.CatalogingStore)
	assert.True(t, ok)
}

const ordersSpec = `
openapi: 3.0.3
info:
  title: Orders
  version: "1"
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        '200':
          description: OK
`

func TestAPIReaderListsTheStoredOperations(t *testing.T) {
	cfg, err := apigateway.ParseConfig(map[string]any{"base_url": "https://orders.example.com", "catalog_id": "orders-v1"})
	require.NoError(t, err)
	tk := apigateway.NewMulti(apigateway.MultiConfig{DefaultName: "api", Instances: map[string]apigateway.Config{"orders": cfg}})
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	store := apicatalog.NewMemoryStore()
	require.NoError(t, store.CreateCatalog(context.Background(), apicatalog.Catalog{ID: "orders-v1", Name: "orders-v1", DisplayName: "o"}))
	require.NoError(t, store.UpsertSpec(context.Background(), "orders-v1",
		apicatalog.SpecEntry{SpecName: "orders", Content: ordersSpec, SourceKind: apicatalog.SourceInline}))
	apigwwiring.CatalogStore(reg, store)

	r := apiReader{reg: reg}
	require.Len(t, r.Connections(), 1)
	ops, err := r.Operations(context.Background(), "orders")
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, []string{"listOrders"}, ops[0].Path)
	assert.Equal(t, "orders", ops[0].Document, "an operation names the spec it was read from")

	docs, err := r.Documents(context.Background(), "orders")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "orders", docs[0].Key)
	assert.Equal(t, "orders.yaml", docs[0].Filename)
	assert.Equal(t, "application/yaml", docs[0].MIMEType)
	assert.Equal(t, ordersSpec, string(docs[0].Content))
	_, err = r.Documents(context.Background(), "nope")
	assert.Error(t, err)
}

// specDocuments reads a JSON spec as JSON, skips a catalog's GraphQL schema,
// and reads nothing for a connection with no catalog.
func TestSpecDocuments(t *testing.T) {
	ctx := context.Background()
	store := apicatalog.NewMemoryStore()
	require.NoError(t, store.CreateCatalog(ctx, apicatalog.Catalog{ID: "c", Name: "c", DisplayName: "c"}))
	require.NoError(t, store.UpsertSpec(ctx, "c", apicatalog.SpecEntry{SpecName: "json", Content: `{"openapi":"3.0.0"}`, SourceKind: apicatalog.SourceInline}))
	require.NoError(t, store.UpsertSpec(ctx, "c", apicatalog.SpecEntry{
		SpecName: "gql", Content: "type Query { a: Int }", SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatGraphQL,
	}))
	docs, err := specDocuments(ctx, store, "c")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "json.json", docs[0].Filename)
	assert.Equal(t, "application/json", docs[0].MIMEType)

	docs, err = specDocuments(ctx, nil, "c")
	require.NoError(t, err)
	assert.Empty(t, docs)
	docs, err = specDocuments(ctx, store, "")
	require.NoError(t, err)
	assert.Empty(t, docs)
}
