package graphql

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/useragent"
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

// wafBlockPage is what a web application firewall answers a request it
// refuses with: an HTML page that says nothing about the request.
const wafBlockPage = `<!DOCTYPE html><html><head><title>Access denied</title></head><body><h1>Access denied</h1><p>Request blocked.</p></body></html>`

// wafUpstream is an endpoint that refuses every request with a 403 HTML
// page, recording the User-Agent each one carried, as the endpoint in
// #1679 did to the platform's introspection.
func wafUpstream(t *testing.T) (u *upstream, seen func() []string) {
	t.Helper()
	var mu sync.Mutex
	var agents []string
	u = &upstream{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		agents = append(agents, r.Header.Get("User-Agent"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(wafBlockPage))
	}))
	t.Cleanup(u.server.Close)
	return u, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), agents...)
	}
}

// TestRefreshSchemaSendsTheProductUserAgent is the defect: the
// introspection went out as Go-http-client/1.1. The endpoint records what
// arrived, which is the platform's product string and version.
func TestRefreshSchemaSendsTheProductUserAgent(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := u.lastHeaders().Get("User-Agent"); got != useragent.Product() {
		t.Errorf("the introspection carried User-Agent %q; want %q", got, useragent.Product())
	}
}

// TestRefreshSchemaNamesTheUserAgentWhenAFirewallPageComesBack is the
// diagnosis: a 403 HTML page quoted as the error says nothing, so the
// error names the User-Agent the request went out with and the key that
// changes it. With static_headers pinning one, the error names that.
func TestRefreshSchemaNamesTheUserAgentWhenAFirewallPageComesBack(t *testing.T) {
	t.Run("the product string by default", func(t *testing.T) {
		u, seen := wafUpstream(t)
		tk := newToolkit(t, u, "", nil)
		err := tk.RefreshSchema(context.Background(), "gql")
		if err == nil {
			t.Fatal("a 403 produced no error")
		}
		msg := err.Error()
		if !strings.Contains(msg, `User-Agent was "`+useragent.Product()+`"`) {
			t.Errorf("err = %v; want the product User-Agent named", err)
		}
		if !strings.Contains(msg, "static_headers") || !strings.Contains(msg, `"User-Agent"`) {
			t.Errorf("err = %v; want the static_headers knob named", err)
		}
		if strings.Contains(msg, "<html") {
			t.Errorf("err = %v; the block page must not be the message", err)
		}
		if got := seen(); len(got) != 1 || got[0] != useragent.Product() {
			t.Errorf("the endpoint saw %v; the error must name what was sent", got)
		}
		if info, _ := tk.SchemaInfo("gql"); info.Error != msg {
			t.Errorf("the connection's schema state reports %q; want the same message", info.Error)
		}
	})
	t.Run("the pinned value under static_headers", func(t *testing.T) {
		u, seen := wafUpstream(t)
		tk := newToolkit(t, u, "", map[string]any{"static_headers": map[string]any{"User-Agent": "acme-integrations/2"}})
		err := tk.RefreshSchema(context.Background(), "gql")
		if err == nil || !strings.Contains(err.Error(), `User-Agent was "acme-integrations/2"`) {
			t.Errorf("err = %v; want the pinned User-Agent named", err)
		}
		if got := seen(); len(got) != 1 || got[0] != "acme-integrations/2" {
			t.Errorf("the endpoint saw %v; static_headers must override the product", got)
		}
	})
	t.Run("a 403 that is not a page keeps the upstream's words", func(t *testing.T) {
		u := newUpstream(t)
		u.introspection = `{"errors":[{"message":"forbidden"}]}`
		tk := newToolkit(t, u, "", nil)
		err := introspectionFailure(&execution{status: http.StatusForbidden, body: []byte(`{"message":"token lacks introspection scope"}`), userAgent: "x"})
		if err == nil || !strings.Contains(err.Error(), "token lacks introspection scope") || strings.Contains(err.Error(), "User-Agent") {
			t.Errorf("err = %v; a JSON 403 is quoted as before", err)
		}
		_ = tk
	})
}

// TestLooksLikeHTMLReadsTheOpeningOfTheBody pins what counts as a page: a
// document that declares itself HTML in its opening, however long the page,
// and not a JSON body that happens to mention a tag further in.
func TestLooksLikeHTMLReadsTheOpeningOfTheBody(t *testing.T) {
	longPage := wafBlockPage + strings.Repeat("<p>blocked</p>", 500)
	cases := map[string]struct {
		body []byte
		want bool
	}{
		"a doctype page":      {[]byte(wafBlockPage), true},
		"a page past 1 KiB":   {[]byte(longPage), true},
		"an html tag alone":   {[]byte("  <HTML><body>no</body></HTML>"), true},
		"a graphql response":  {[]byte(`{"errors":[{"message":"forbidden"}]}`), false},
		"a tag past the head": {append([]byte(strings.Repeat(" ", 2048)), []byte("<html>")...), false},
		"empty":               {nil, false},
	}
	for name, tc := range cases {
		if got := looksLikeHTML(tc.body); got != tc.want {
			t.Errorf("%s: looksLikeHTML = %v; want %v", name, got, tc.want)
		}
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
