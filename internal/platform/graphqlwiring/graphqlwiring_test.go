package graphqlwiring

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/platform/routepolicy"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// introspectionResult is what the fake endpoint answers the
// introspection query with: the smallest schema that still exposes an
// operation, so wiring can be observed to have read one.
const introspectionResult = `{"data":{"__schema":{
  "queryType": {"name": "Query"},
  "types": [
    {"kind": "OBJECT", "name": "Query", "fields": [
      {"name": "health", "args": [], "type": {"kind": "SCALAR", "name": "String"}, "isDeprecated": false}
    ]}
  ],
  "directives": []
}}}`

// otherToolkit is a registered toolkit of a different kind, so the
// wiring is proved to pick out its own rather than everything.
type otherToolkit struct{}

func (otherToolkit) Kind() string                          { return "trino" }
func (otherToolkit) Name() string                          { return "warehouse" }
func (otherToolkit) Connection() string                    { return "warehouse" }
func (otherToolkit) Tools() []string                       { return nil }
func (otherToolkit) RegisterTools(*mcp.Server)             {}
func (otherToolkit) SetSemanticProvider(semantic.Provider) {}
func (otherToolkit) SetQueryProvider(query.Provider)       {}
func (otherToolkit) Close() error                          { return nil }

func newRegistry(t *testing.T, endpoint string) *registry.Registry {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": endpoint})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "gql"
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "gql", Instances: map[string]graphqlkit.Config{"gql": cfg},
	})
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register graphql: %v", err)
	}
	if err := reg.Register(otherToolkit{}); err != nil {
		t.Fatalf("register other: %v", err)
	}
	return reg
}

func TestToolkitsPicksOutItsOwnKind(t *testing.T) {
	reg := newRegistry(t, "https://unreached.invalid/graphql")
	got := Toolkits(reg)
	if len(got) != 1 || got[0].Kind() != graphqlkit.Kind {
		t.Errorf("toolkits = %+v", got)
	}
	if Toolkits(nil) != nil {
		t.Error("a nil registry produced toolkits")
	}
}

func TestWireReadsEachConnectionsSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(introspectionResult))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)

	Wire(context.Background(), Deps{Registry: reg})

	tk := Toolkits(reg)[0]
	info, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if info.OperationCount != 1 || info.Source != graphqlkit.SchemaSourceIntrospection {
		t.Errorf("info = %+v; wiring is what brings a connection's schema up", info)
	}
}

func TestWireRecordsWhyASchemaCouldNotBeRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"introspection disabled"}]}`))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)

	// An endpoint that will not answer must not stop the platform: the
	// connection registers with its cause recorded.
	Wire(context.Background(), Deps{Registry: reg})

	info, err := Toolkits(reg)[0].SchemaInfo("gql")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if !strings.Contains(info.Error, "introspection disabled") {
		t.Errorf("info = %+v", info)
	}
}

func TestWireIsANoOpWithoutAGraphQLToolkit(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register(otherToolkit{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	Wire(context.Background(), Deps{Registry: reg})
	Wire(context.Background(), Deps{})
}

func TestAttachExportAttachesTheExportDependenciesItWasGiven(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(introspectionResult))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)

	AttachExport(reg, &graphqlkit.ExportDeps{})

	tools := Toolkits(reg)[0].Tools()
	if len(tools) != 3 {
		t.Errorf("tools = %v; export is registered once the platform wires it", tools)
	}
}

// TestAttachExportWithoutDependenciesLeavesTheToolUnregistered covers the
// deployment with no portal: the kind still runs, with the export tool absent
// from the toolkit's list rather than present and unreachable.
func TestAttachExportWithoutDependenciesLeavesTheToolUnregistered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(introspectionResult))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)

	AttachExport(reg, nil)
	AttachExport(nil, &graphqlkit.ExportDeps{})

	tools := Toolkits(reg)[0].Tools()
	if len(tools) != 2 {
		t.Errorf("tools = %v; want the two unconditional tools", tools)
	}
}

// stubEmbedder and stubOAuthStore stand in for dependencies whose own
// behavior is covered elsewhere; here they only have to be non-nil so
// the branch that attaches each is taken.
type stubEmbedder struct{}

func (stubEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (stubEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}
func (stubEmbedder) Dimension() int { return 1 }
func (stubEmbedder) Kind() string   { return "stub" }

type stubOAuthStore struct{}

func (stubOAuthStore) Get(context.Context, connoauth.Key) (*connoauth.PersistedToken, error) {
	return nil, connoauth.ErrTokenNotFound
}
func (stubOAuthStore) Set(context.Context, connoauth.PersistedToken) error      { return nil }
func (stubOAuthStore) Delete(context.Context, connoauth.Key) error              { return nil }
func (stubOAuthStore) List(context.Context) ([]connoauth.PersistedToken, error) { return nil, nil }
func (stubOAuthStore) Lock(context.Context, connoauth.Key) (func(), error) {
	return func() {}, nil
}

// TestWireAttachesEveryOptionalDependency builds every optional dependency Wire attaches, so the branch
// that installs each one is exercised and a connection is still usable
// with all of them present.
func TestWireAttachesEveryOptionalDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(introspectionResult))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)
	metrics, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()
	// The wiring writes the schema it read; the store's own SQL is
	// covered by its package, so here it only has to be reachable.
	mock.ExpectExec("INSERT INTO graphql_connection_schemas").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT operation_id, embedding").
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "embedding"}))
	mock.ExpectQuery("SELECT schema_hash").WillReturnError(sql.ErrNoRows)

	Wire(context.Background(), Deps{
		Registry:    reg,
		DB:          db,
		Embedder:    stubEmbedder{},
		RoutePolicy: routepolicy.New(routepolicy.Deps{}),
		OAuthStore:  stubOAuthStore{},
		AuthEvents:  authevents.NewWriter(nil, nil),
		MemBudget:   membudget.New(1 << 20),
		Metrics:     metrics,
	})

	tk := Toolkits(reg)[0]
	if tk.ConnOAuthStore() == nil {
		t.Error("the OAuth token store was not attached")
	}
	// With a database wired, the schema read is also persisted, so the
	// connection comes back from the store rather than the endpoint.
	info, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if info.OperationCount != 1 {
		t.Errorf("info = %+v", info)
	}
}
