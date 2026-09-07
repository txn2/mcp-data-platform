package graphql

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRefreshSchemaReadsTheEndpointAndStoresWhatItRead(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	store := newMemorySchemaStore()
	tk.SetSchemaStore(store)

	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	info, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if info.Source != SchemaSourceIntrospection {
		t.Errorf("source = %q; want introspection", info.Source)
	}
	if info.OperationCount != 2 {
		t.Errorf("operations = %d; the fixture exposes dataset and retire", info.OperationCount)
	}
	if info.Hash == "" || info.FetchedAt.IsZero() || info.Error != "" {
		t.Errorf("info = %+v", info)
	}
	stored, err := store.GetSchema(context.Background(), "gql")
	if err != nil {
		t.Fatalf("the schema was not stored: %v", err)
	}
	if stored.Hash != info.Hash || !strings.Contains(stored.SDL, "dataset") {
		t.Errorf("stored = %+v", stored)
	}
}

func TestRefreshSchemaNamesTheDisabledIntrospectionCase(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	err := tk.RefreshSchema(context.Background(), "gql")
	if err == nil {
		t.Fatal("an endpoint that refuses introspection produced no error")
	}
	if !strings.Contains(err.Error(), "introspection is not allowed") {
		t.Errorf("err = %v; the upstream's own words are the diagnosis", err)
	}
	info, _ := tk.SchemaInfo("gql")
	if info.Error == "" {
		t.Error("the cause was not recorded on the connection")
	}
	if info.OperationCount != 0 {
		t.Error("an operation index appeared for a schema that was never read")
	}
}

func TestRefreshSchemaReportsATransportOrStatusFailure(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	// An endpoint the platform cannot reach at all: the connection is
	// registered, its schema is not, and the cause is recorded.
	u.server.Close()
	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Error("want a refusal")
	}
	if info, _ := tk.SchemaInfo("gql"); info.Error == "" {
		t.Error("the transport failure was not recorded on the connection")
	}
	if err := tk.RefreshSchema(context.Background(), "missing"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("refreshing an unknown connection gave %v", err)
	}
}

func TestRefreshSchemaRefusesAnIntrospectionResultPastTheReadCap(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", map[string]any{"max_response_bytes": 64})
	err := tk.RefreshSchema(context.Background(), "gql")
	if err == nil || !strings.Contains(err.Error(), "max_response_bytes") {
		t.Errorf("err = %v; want the knob named", err)
	}
}

func TestSetSchemaAcceptsBothFormsAnOperatorSupplies(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("SDL: %v", err)
	}
	fromSDL, _ := tk.SchemaInfo("gql")
	if fromSDL.Source != SchemaSourceUpload {
		t.Errorf("source = %q; want upload", fromSDL.Source)
	}
	if err := tk.SetSchema(context.Background(), "gql", []byte(flatIntrospectionResult)); err != nil {
		t.Fatalf("introspection JSON: %v", err)
	}
	fromJSON, _ := tk.SchemaInfo("gql")
	if fromJSON.Hash == fromSDL.Hash {
		t.Error("the second upload did not replace the first")
	}
	if err := tk.SetSchema(context.Background(), "gql", []byte("type Query {")); err == nil {
		t.Error("malformed SDL was accepted")
	}
	if err := tk.SetSchema(context.Background(), "missing", fixtureSDL(t, "flat")); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("uploading to an unknown connection gave %v", err)
	}
}

func TestHydrateSchemasPrefersTheStoredSchemaOverAnEndpointRead(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	store := newMemorySchemaStore()
	tk.SetSchemaStore(store)
	if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := len(u.calls())

	tk.HydrateSchemas(context.Background())

	if got := len(u.calls()); got != before {
		t.Errorf("hydration read the endpoint %d extra times; the stored schema is what a restart uses", got-before)
	}
	info, _ := tk.SchemaInfo("gql")
	if info.OperationCount < 6 {
		t.Errorf("the namespaced schema was not the one loaded: %+v", info)
	}
}

func TestHydrateSchemasFallsBackToTheEndpoint(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaStore(newMemorySchemaStore())

	tk.HydrateSchemas(context.Background())

	info, _ := tk.SchemaInfo("gql")
	if info.Source != SchemaSourceIntrospection || info.OperationCount == 0 {
		t.Errorf("info = %+v; an empty store means read the endpoint", info)
	}
}

func TestHydrateSchemasSurvivesAStoreThatCannotAnswer(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	store := newMemorySchemaStore()
	store.getErr = errors.New("database is down")
	store.putErr = errors.New("database is down")
	tk.SetSchemaStore(store)

	tk.HydrateSchemas(context.Background())

	// A storage failure must not cost the connection its schema: it is
	// usable with what it read, and persistence catches up later.
	info, _ := tk.SchemaInfo("gql")
	if info.OperationCount == 0 {
		t.Errorf("info = %+v; a store failure took the connection down", info)
	}
}

func TestHydrateSchemasRereadsAStoredSchemaThatNoLongerParses(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	store := newMemorySchemaStore()
	store.schemas["gql"] = StoredSchema{Connection: "gql", Hash: "x", SDL: "type Query {", Source: SchemaSourceUpload}
	tk.SetSchemaStore(store)

	tk.HydrateSchemas(context.Background())

	info, _ := tk.SchemaInfo("gql")
	if info.Source != SchemaSourceIntrospection {
		t.Errorf("info = %+v; an unusable stored schema falls back to the endpoint", info)
	}
}

func TestSchemaInfoAndOperationsOnAConnectionWithNoSchema(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	if _, err := tk.SchemaInfo("missing"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("unknown connection gave %v", err)
	}
	if _, _, ok := tk.Operations("gql"); ok {
		t.Error("operations were reported for a connection with no schema")
	}
	if _, _, ok := tk.IndexItems("gql"); ok {
		t.Error("index items were reported for a connection with no schema")
	}
	if _, _, ok := tk.Operations("missing"); ok {
		t.Error("operations were reported for an unknown connection")
	}
	if _, _, ok := tk.IndexItems("missing"); ok {
		t.Error("index items were reported for an unknown connection")
	}
}

func TestSchemaInfosReportsEveryConnection(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	if err := tk.AddConnection("second", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	infos := tk.SchemaInfos()
	if len(infos) != 2 {
		t.Fatalf("infos = %+v", infos)
	}
	if infos[0].Connection != "gql" || infos[1].Connection != "second" {
		t.Errorf("infos are not in connection order: %+v", infos)
	}
}

func TestIndexItemsCarriesOneTextPerOperation(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	hash, items, ok := tk.IndexItems("gql")
	if !ok {
		t.Fatal("no index items")
	}
	if hash == "" {
		t.Error("the items are not tied to a schema version")
	}
	text, ok := items["query:masterData.product.query"]
	if !ok {
		t.Fatalf("items = %v", keys(items))
	}
	if !strings.Contains(text, "List products") {
		t.Errorf("index text = %q", text)
	}
}

func TestReloadVectorsPicksUpAnIndexThatRanLater(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	_, _, ok := tk.Operations("gql")
	if !ok {
		t.Fatal("no operations")
	}
	tk.SetVectorReader(memoryVectors{vectors: map[string][]float32{"query:dataset": {1, 0}}})
	tk.SetEmbeddingProvider(wordEmbedder{dim: 8})

	c, _, _ := tk.lookup("gql")
	if tk.embeddingsAvailable(c) {
		t.Error("vectors were visible before the reload")
	}
	tk.ReloadVectors(context.Background(), "gql")
	if !tk.embeddingsAvailable(c) {
		t.Error("the reload did not pick up the persisted vectors")
	}
	// A connection with no schema has no version to load vectors for.
	tk.ReloadVectors(context.Background(), "missing")
}

func TestLoadVectorsSurvivesAReaderThatFails(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetVectorReader(memoryVectors{err: errors.New("database is down")})
	tk.ReloadVectors(context.Background(), "gql")
	c, _, _ := tk.lookup("gql")
	if err := embeddingsReady(c); err == nil {
		t.Error("a failed vector read was treated as an index")
	}
}

func TestSnippetBoundsAnUpstreamsRawBody(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := snippet(long); len(got) >= len(long) {
		t.Errorf("snippet did not bound a long body: %d chars", len(got))
	}
	if got := snippet("  short  "); got != "short" {
		t.Errorf("snippet = %q", got)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
