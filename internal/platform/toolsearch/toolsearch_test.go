package toolsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// stubEmbedder is a configured provider returning a fixed non-zero vector.
type stubEmbedder struct{}

func (stubEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil }

func (stubEmbedder) EmbedBatch(_ context.Context, t []string) ([][]float32, error) {
	return make([][]float32, len(t)), nil
}

func (stubEmbedder) Dimension() int { return 3 }

func (stubEmbedder) Kind() string { return "fake" }

func corpus() []*mcp.Tool {
	return []*mcp.Tool{
		{Name: "search", Description: "Search everything."},
		{Name: "manage_site", Description: "Publish a static site and manage its releases.", InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "enum": []any{"create", "publish", "list"}},
				"site":    map[string]any{"type": "string", "description": "publish: the site. Required for publish."},
			},
		}},
		{Name: "manage_asset", Description: "Manage portal assets.", InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{"get", "list", "search", "delete"}},
				"scope":  map[string]any{"type": "string", "enum": []any{"mine", "shared", "all"}, "description": "Which assets list and search cover."},
			},
		}},
		{Name: "trino_query", Description: "Run read-only SQL.", InputSchema: map[string]any{
			"type": "object", "required": []any{"sql"},
			"properties": map[string]any{"sql": map[string]any{"type": "string"}},
		}},
	}
}

func ranker() *Ranker {
	tools := corpus()
	return &Ranker{
		Enumerate: func(context.Context) ([]*mcp.Tool, error) { return tools, nil },
		Exclude:   []string{"search"},
	}
}

func TestRankTools_LexicalWithExamples(t *testing.T) {
	got := ranker().RankTools(context.Background(), "publish a static site", nil, 5)
	if len(got) == 0 || got[0].Name != "manage_site" {
		t.Fatalf("first tool = %+v, want manage_site", got)
	}
	if got[0].Example["command"] != "publish" || got[0].Example["site"] != "<string>" {
		t.Errorf("example = %v, want command publish with its required site", got[0].Example)
	}

	shared := ranker().RankTools(context.Background(), "assets shared with me", nil, 5)
	if len(shared) == 0 || shared[0].Name != "manage_asset" {
		t.Fatalf("first tool = %+v, want manage_asset", shared)
	}
	if shared[0].Example["action"] != "list" || shared[0].Example["scope"] != "shared" {
		t.Errorf("example = %v, want action list with scope shared", shared[0].Example)
	}
}

func TestRankTools_ExcludesSearchAndFiltersByPersona(t *testing.T) {
	r := ranker()
	r.Allowed = func(_ context.Context, _ []string, tool string) bool { return tool != "manage_site" }
	for _, m := range r.RankTools(context.Background(), "search publish site", []string{"analyst"}, 10) {
		if m.Name == "search" || m.Name == "manage_site" {
			t.Errorf("returned %s", m.Name)
		}
	}
	// With no roles every tool is allowed, as tools/list does.
	got := r.RankTools(context.Background(), "publish a static site", nil, 10)
	if len(got) == 0 || got[0].Name != "manage_site" {
		t.Errorf("no-role caller lost manage_site: %+v", got)
	}
}

func TestRankTools_EdgeCases(t *testing.T) {
	var nilRanker *Ranker
	if nilRanker.RankTools(context.Background(), "x", nil, 5) != nil {
		t.Error("a nil ranker ranked")
	}
	if ranker().RankTools(context.Background(), "  ", nil, 5) != nil {
		t.Error("an empty intent ranked")
	}
	failing := &Ranker{Enumerate: func(context.Context) ([]*mcp.Tool, error) { return nil, errors.New("down") }}
	if failing.RankTools(context.Background(), "x", nil, 5) != nil {
		t.Error("a failed listing ranked")
	}
	if got := ranker().RankTools(context.Background(), "publish site query", nil, 1); len(got) != 1 {
		t.Errorf("limit not applied: %d", len(got))
	}
}

func TestRankTools_SemanticPath(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("ORDER BY embedding").WillReturnRows(sqlmock.NewRows([]string{"tool_name", "score"}).
		AddRow("trino_query", 0.9).AddRow("search", 0.8).AddRow("unknown_tool", 0.7))
	r := ranker()
	r.Embedder = func() embedding.Provider { return stubEmbedder{} }
	store := toolsindex.NewStore(db)
	r.Index = func() *toolsindex.Store { return store }
	got := r.RankTools(context.Background(), "run a query", nil, 5)
	if len(got) != 1 || got[0].Name != "trino_query" || got[0].Score != 0.9 {
		t.Fatalf("semantic = %+v, want only trino_query@0.9", got)
	}
	if got[0].Example["sql"] != "<string>" {
		t.Errorf("example = %v", got[0].Example)
	}

	// An unconfigured embedder falls back to lexical.
	r.Embedder = func() embedding.Provider { return embedding.NewNoopProvider(3) }
	if got := r.RankTools(context.Background(), "run read-only sql", nil, 5); len(got) == 0 || got[0].Name != "trino_query" {
		t.Errorf("fallback = %+v", got)
	}
}

func TestSchemaOfAndZero(t *testing.T) {
	if schemaOf(nil) != nil || schemaOf(&mcp.Tool{}) != nil {
		t.Error("a tool with no schema has one")
	}
	if schemaOf(&mcp.Tool{InputSchema: struct {
		Type string `json:"type"`
	}{"object"}})["type"] != "object" {
		t.Error("a struct schema did not decode")
	}
	if !zero([]float32{0, 0}) || zero([]float32{0, 1}) {
		t.Error("zero misread")
	}
}
