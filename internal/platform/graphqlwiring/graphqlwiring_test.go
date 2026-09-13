package graphqlwiring

import (
	"context"
	"database/sql"
	"errors"
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

// savedConfigs is a graphqlkit.ConnectionStore holding what another
// replica saved.
type savedConfigs map[string]map[string]any

func (s savedConfigs) GetConnection(_ context.Context, name string) (map[string]any, error) {
	cfg, ok := s[name]
	if !ok {
		return nil, graphqlkit.ErrConnectionNotFound
	}
	return cfg, nil
}

// record is a connection store's record in these tests.
type record struct{ config map[string]any }

var errNoRecord = errors.New("no record")

// recordStore is a RecordStore over a map, recording the kinds read.
type recordStore struct {
	records    map[string]*record
	err        error
	persistent bool
	kinds      []string
}

func (s *recordStore) Get(_ context.Context, kind, name string) (*record, error) {
	s.kinds = append(s.kinds, kind)
	if s.err != nil {
		return nil, s.err
	}
	r, ok := s.records[name]
	if !ok {
		return nil, errNoRecord
	}
	return r, nil
}

func (s *recordStore) Persistent() bool { return s.persistent }

func TestSavedConnectionsReadsTheGraphQLKindOutOfAStoreThatPersists(t *testing.T) {
	config := func(r *record) map[string]any { return r.config }
	if SavedConnections[*record](nil, errNoRecord, config) != nil {
		t.Error("a nil store adapted to a connection store")
	}
	if SavedConnections[*record](&recordStore{}, errNoRecord, config) != nil {
		t.Error("a store that does not persist adapted to a connection store")
	}

	store := &recordStore{persistent: true, records: map[string]*record{"erp": {config: map[string]any{"endpoint_url": "https://erp.example.com/graphql"}}}}
	saved := SavedConnections[*record](store, errNoRecord, config)
	got, err := saved.GetConnection(context.Background(), "erp")
	if err != nil || got["endpoint_url"] != "https://erp.example.com/graphql" {
		t.Errorf("GetConnection = %v, %v", got, err)
	}
	if len(store.kinds) != 1 || store.kinds[0] != graphqlkit.Kind {
		t.Errorf("read kinds %v; want only %q", store.kinds, graphqlkit.Kind)
	}
	if _, err := saved.GetConnection(context.Background(), "absent"); !errors.Is(err, graphqlkit.ErrConnectionNotFound) {
		t.Errorf("an absent record gave %v; want ErrConnectionNotFound", err)
	}
	store.err = errors.New("db unavailable")
	if _, err := saved.GetConnection(context.Background(), "erp"); err == nil || errors.Is(err, graphqlkit.ErrConnectionNotFound) {
		t.Errorf("a store failure gave %v; want a failure that is not a missing connection", err)
	}
}

func TestWireServesAConnectionAnotherReplicaSaved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(introspectionResult))
	}))
	defer server.Close()
	reg := newRegistry(t, server.URL)
	tk := Toolkits(reg)[0]
	if tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("a connection nobody handed over was served before wiring")
	}

	Wire(context.Background(), Deps{Registry: reg, Connections: savedConfigs{"saved": {"endpoint_url": server.URL}}})

	if !tk.ServesConnection(context.Background(), "saved") {
		t.Error("wiring did not give the toolkit the connection store")
	}
}

// memoryStore is a graphqlkit.SchemaStore over a map: what a peer replica
// wrote, as this replica reads it.
type memoryStore struct {
	schemas map[string]graphqlkit.StoredSchema
}

func (m *memoryStore) GetSchema(_ context.Context, connection string) (graphqlkit.StoredSchema, error) {
	s, ok := m.schemas[connection]
	if !ok {
		return graphqlkit.StoredSchema{}, graphqlkit.ErrSchemaNotFound
	}
	return s, nil
}

func (m *memoryStore) SchemaVersion(ctx context.Context, connection string) (graphqlkit.StoredSchema, error) {
	s, err := m.GetSchema(ctx, connection)
	s.SDL = ""
	return s, err
}

func (m *memoryStore) PutSchema(_ context.Context, s graphqlkit.StoredSchema) error {
	m.schemas[s.Connection] = s
	return nil
}

func (m *memoryStore) RecordReadError(_ context.Context, held graphqlkit.StoredSchema) error {
	if stored, ok := m.schemas[held.Connection]; ok && stored.Hash == held.Hash && stored.FetchedAt.Equal(held.FetchedAt) {
		stored.ReadError = held.ReadError
		m.schemas[held.Connection] = stored
	}
	return nil
}

func (m *memoryStore) DeleteSchema(_ context.Context, connection string) error {
	delete(m.schemas, connection)
	return nil
}

// TestReloadStoredSchemaInstallsWhatAPeerStored: a peer's announcement lands
// the store's schema on every graphql toolkit holding the connection, and on
// no other (#1676).
func TestReloadStoredSchemaInstallsWhatAPeerStored(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"introspection disabled"}]}`))
	}))
	defer refusing.Close()
	reg := newRegistry(t, refusing.URL)
	store := &memoryStore{schemas: map[string]graphqlkit.StoredSchema{}}
	Wire(context.Background(), Deps{Registry: reg})
	tk := Toolkits(reg)[0]
	tk.SetSchemaStore(store)

	// The peer's upload, as the store holds it.
	peer := graphqlkit.NewMulti(graphqlkit.MultiConfig{})
	peer.SetSchemaStore(store)
	if err := peer.AddConnection("gql", map[string]any{"endpoint_url": refusing.URL}); err != nil {
		t.Fatalf("peer add: %v", err)
	}
	if err := peer.SetSchema(context.Background(), "gql", []byte("schema { query: Query } type Query { health: String }")); err != nil {
		t.Fatalf("peer upload: %v", err)
	}
	if before, _ := tk.SchemaInfo("gql"); before.OperationCount != 0 || before.Error == "" {
		t.Fatalf("this replica's own read did not fail: %+v", before)
	}

	ReloadStoredSchema(context.Background(), reg, "gql")

	after, _ := tk.SchemaInfo("gql")
	if after.Source != graphqlkit.SchemaSourceUpload || after.OperationCount != 1 || after.Error != "" {
		t.Errorf("info = %+v; the peer's upload is what this replica serves", after)
	}
	// A connection this registry does not hold, and a registry with nothing
	// to reload, are left alone; so is a connection whose row is gone by the
	// time the announcement lands, which keeps what it holds.
	ReloadStoredSchema(context.Background(), reg, "absent")
	ReloadStoredSchema(context.Background(), nil, "gql")
	delete(store.schemas, "gql")
	ReloadStoredSchema(context.Background(), reg, "gql")
	if kept, _ := tk.SchemaInfo("gql"); kept.Hash != after.Hash {
		t.Errorf("an announcement with no row behind it changed the connection: %+v", kept)
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
