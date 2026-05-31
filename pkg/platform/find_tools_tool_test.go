package platform

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// stubFindEmbedder is a configured (non-noop) embedding provider that
// returns a fixed non-zero vector, so rankFindTools takes the semantic
// path.
type stubFindEmbedder struct{ dim int }

func (e *stubFindEmbedder) Embed(context.Context, string) ([]float32, error) {
	v := make([]float32, e.dim)
	v[0] = 1
	return v, nil
}

func (e *stubFindEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
	}
	return out, nil
}
func (e *stubFindEmbedder) Dimension() int { return e.dim }
func (*stubFindEmbedder) Kind() string     { return "fake" }

func tool(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }

func TestToolParamSummary(t *testing.T) {
	t.Parallel()
	if got := toolParamSummary(nil); got != "" {
		t.Errorf("nil schema = %q; want empty", got)
	}
	schema := map[string]any{
		"properties": map[string]any{
			"query": map[string]any{"description": "the query"},
			"limit": map[string]any{},
		},
	}
	got := toolParamSummary(schema)
	want := "limit, query (the query)" // sorted by name
	if got != want {
		t.Errorf("summary = %q; want %q", got, want)
	}
	// Unmarshalable value (chan) -> marshal error -> empty.
	if s := toolParamSummary(make(chan int)); s != "" {
		t.Errorf("unmarshalable schema = %q; want empty", s)
	}
	// Non-object schema -> unmarshal into the properties struct fails -> empty.
	if s := toolParamSummary(42); s != "" {
		t.Errorf("non-object schema = %q; want empty", s)
	}
}

func TestToolEmbedText(t *testing.T) {
	t.Parallel()
	if got := toolEmbedText(&mcp.Tool{Name: "x"}); got != "x" {
		t.Errorf("name-only = %q; want x", got)
	}
	got := toolEmbedText(&mcp.Tool{Name: "trino_query", Description: "run SQL"})
	if got != "trino_query\nrun SQL" {
		t.Errorf("name+desc = %q", got)
	}
}

func TestZeroVector(t *testing.T) {
	t.Parallel()
	if !zeroVector([]float32{0, 0, 0}) {
		t.Error("all-zero should be zero vector")
	}
	if zeroVector([]float32{0, 1, 0}) {
		t.Error("non-zero should not be zero vector")
	}
}

func TestLexicalFindTools(t *testing.T) {
	t.Parallel()
	desc := map[string]*mcp.Tool{
		"trino_query":         tool("trino_query", "run SQL against Trino"),
		"s3_list_objects":     tool("s3_list_objects", "list objects in a bucket"),
		"denied_tool":         tool("denied_tool", "should be filtered by persona"),
		platformFindToolsName: tool(platformFindToolsName, "discovery"),
	}
	permit := func(n string) bool { return n != "denied_tool" }

	// Query match on description; denied + self excluded.
	got := lexicalFindTools("sql", desc, permit, 10)
	if len(got) != 1 || got[0].Name != "trino_query" {
		t.Errorf("query 'sql' = %+v; want [trino_query]", got)
	}

	// Empty query lists permitted tools (sorted), excluding self + denied.
	all := lexicalFindTools("", desc, permit, 10)
	names := make([]string, len(all))
	for i, d := range all {
		names[i] = d.Name
	}
	if len(names) != 2 || names[0] != "s3_list_objects" || names[1] != "trino_query" {
		t.Errorf("empty query = %v; want [s3_list_objects trino_query]", names)
	}

	// Limit cap.
	if capped := lexicalFindTools("", desc, permit, 1); len(capped) != 1 {
		t.Errorf("limit 1 returned %d", len(capped))
	}
}

func TestToolPermitter_NoAuthorizer(t *testing.T) {
	t.Parallel()
	p := &Platform{} // no authorizer
	permit := p.toolPermitter(context.Background())
	if !permit("anything") {
		t.Error("with no authorizer every tool should be permitted (mirrors visibility middleware)")
	}
}

func TestRankFindTools_FallbackWhenNoIndex(t *testing.T) {
	t.Parallel()
	// No embedder + no store -> lexical fallback.
	p := &Platform{}
	desc := map[string]*mcp.Tool{"trino_query": tool("trino_query", "run SQL")}
	out := p.rankFindTools(context.Background(), "sql", 10, desc, func(string) bool { return true })
	if len(out.Tools) != 1 || out.Tools[0].Name != "trino_query" {
		t.Errorf("fallback tools = %+v", out.Tools)
	}
}

func TestRankFindTools_SemanticPath(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{
		embeddingProv:   &stubFindEmbedder{dim: 3},
		toolsIndexStore: toolsindex.NewStore(db),
	}
	// Ranked: trino_query (visible+permitted), hidden_tool (not in
	// descByName -> dropped), denied_tool (permit=false -> dropped).
	mock.ExpectQuery("ORDER BY embedding").
		WillReturnRows(sqlmock.NewRows([]string{"tool_name", "score"}).
			AddRow("trino_query", 0.9).
			AddRow("hidden_tool", 0.8).
			AddRow("denied_tool", 0.7))

	desc := map[string]*mcp.Tool{
		"trino_query": tool("trino_query", "run SQL"),
		"denied_tool": tool("denied_tool", "secret"),
	}
	permit := func(n string) bool { return n != "denied_tool" }

	out := p.rankFindTools(context.Background(), "run a query", 10, desc, permit)
	if out.Note != "" {
		t.Errorf("semantic path should have no fallback note; got %q", out.Note)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "trino_query" || out.Tools[0].Score != 0.9 {
		t.Errorf("ranked = %+v; want only trino_query@0.9 (hidden/denied filtered)", out.Tools)
	}
}

// findToolsTestServer returns an in-memory MCP server with the given
// tool names registered (plus the discovery tool), for handler tests.
func findToolsTestServer(names ...string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	noop := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	}
	for _, n := range names {
		mcp.AddTool(srv, &mcp.Tool{Name: n, Description: "desc of " + n}, noop)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: platformFindToolsName, Description: "discovery"}, noop)
	return srv
}

func TestHandleFindTools_LexicalFallback(t *testing.T) {
	t.Parallel()
	p := &Platform{mcpServer: findToolsTestServer("alpha", "beta")} // no embedder/store -> lexical
	res, _, err := p.handleFindTools(context.Background(), nil, findToolsInput{Query: "alpha"})
	if err != nil {
		t.Fatalf("handleFindTools: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "alpha") {
		t.Errorf("result should mention alpha: %s", resultText(res))
	}
}

func TestHandleFindTools_EnumerateError(t *testing.T) {
	t.Parallel()
	p := &Platform{} // nil mcpServer -> enumerate fails -> error result
	res, _, err := p.handleFindTools(context.Background(), nil, findToolsInput{Query: "x"})
	if err != nil {
		t.Fatalf("handleFindTools returned go error; want tool-error result: %v", err)
	}
	if !res.IsError {
		t.Error("nil server should produce an IsError result")
	}
}

func TestHandleFindTools_Semantic(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{
		mcpServer:       findToolsTestServer("alpha", "beta"),
		embeddingProv:   &stubFindEmbedder{dim: 3},
		toolsIndexStore: toolsindex.NewStore(db),
	}
	mock.ExpectQuery("ORDER BY embedding").
		WillReturnRows(sqlmock.NewRows([]string{"tool_name", "score"}).AddRow("alpha", 0.95))
	res, _, err := p.handleFindTools(context.Background(), nil, findToolsInput{Query: "do alpha", Limit: 5})
	if err != nil {
		t.Fatalf("handleFindTools: %v", err)
	}
	if res.IsError || !strings.Contains(resultText(res), "alpha") {
		t.Errorf("semantic result wrong: err=%v text=%s", res.IsError, resultText(res))
	}
}

func TestBootstrapToolsIndex(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{
		indexJobsStore:  indexjobs.NewPostgresStore(db),
		toolsIndexStore: toolsindex.NewStore(db),
	}
	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))
	p.bootstrapToolsIndex(context.Background())

	// No-op when not wired.
	(&Platform{}).bootstrapToolsIndex(context.Background())
}
