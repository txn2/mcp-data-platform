package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	fallbackTestSchema = "analytics"
	fallbackTestTable  = "orders_v2"
	fallbackTestQuery  = fallbackTestTable + " " + fallbackTestSchema
	fallbackTestURN    = "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.orders,PROD)"
)

// trinoDescribeRequest builds the minimal request shape that
// enrichTrinoResult uses to pick a table identifier from a single-
// table tool call (the path issue #444 targets first).
func trinoDescribeRequest(t *testing.T, table string) mcp.CallToolRequest {
	t.Helper()
	args, err := json.Marshal(map[string]any{"table": table})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "trino_describe_table",
			Arguments: args,
		},
	}
}

func TestBuildSemanticFallbackQuery(t *testing.T) {
	cases := []struct {
		name  string
		table semantic.TableIdentifier
		want  string
	}{
		{"table_and_schema", semantic.TableIdentifier{Schema: "s", Table: "t"}, "t s"},
		{"table_only", semantic.TableIdentifier{Table: "t"}, "t"},
		{"schema_only", semantic.TableIdentifier{Schema: "s"}, "s"},
		{"empty_yields_empty_query", semantic.TableIdentifier{}, ""},
		{"catalog_ignored_for_recall", semantic.TableIdentifier{Catalog: "hive", Schema: "s", Table: "t"}, "t s"},
	}
	for _, tc := range cases {
		got := buildSemanticFallbackQuery(tc.table)
		if got != tc.want {
			t.Errorf("buildSemanticFallbackQuery(%+v) = %q; want %q", tc.table, got, tc.want)
		}
	}
}

// TestTrySemanticFallback_Disabled proves the fallback is opt-in.
// With the config flag off, no SearchTables call must happen even
// when the provider would gladly serve one. This is the "default
// off" half of acceptance criterion #1 in issue #444.
func TestTrySemanticFallback_Disabled(t *testing.T) {
	called := false
	provider := &mockSemanticProvider{
		searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			called = true
			return []semantic.TableSearchResult{{URN: fallbackTestURN, Name: fallbackTestTable}}, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: false, SemanticFallbackTopK: 3},
	}
	got := enricher.trySemanticFallback(context.Background(), semantic.TableIdentifier{Schema: fallbackTestSchema, Table: fallbackTestTable})
	if got != nil {
		t.Errorf("trySemanticFallback returned %v; want nil when disabled", got)
	}
	if called {
		t.Error("SearchTables was called when fallback is disabled")
	}
}

// TestTrySemanticFallback_EnabledHits proves the enabled path
// queries with the expected mode (semantic) and respects top_k.
func TestTrySemanticFallback_EnabledHits(t *testing.T) {
	var seenFilter semantic.SearchFilter
	provider := &mockSemanticProvider{
		searchTablesFunc: func(_ context.Context, f semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			seenFilter = f
			return []semantic.TableSearchResult{
				{URN: fallbackTestURN, Name: "orders", Platform: "trino", Description: "Order facts"},
				{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.orders_old,PROD)", Name: "orders_old"},
			}, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: true, SemanticFallbackTopK: 2},
	}
	got := enricher.trySemanticFallback(context.Background(), semantic.TableIdentifier{Schema: fallbackTestSchema, Table: fallbackTestTable})
	if len(got) != 2 {
		t.Fatalf("got %d results; want 2", len(got))
	}
	if seenFilter.Mode != fallbackSearchMode {
		t.Errorf("filter.Mode = %q; want %q", seenFilter.Mode, fallbackSearchMode)
	}
	if seenFilter.Limit != 2 {
		t.Errorf("filter.Limit = %d; want 2 (top_k)", seenFilter.Limit)
	}
	if seenFilter.Query != fallbackTestQuery {
		t.Errorf("filter.Query = %q; want %q", seenFilter.Query, fallbackTestQuery)
	}
}

// TestTrySemanticFallback_TopKClampsBelowOne protects against a
// misconfigured EnrichmentConfig where SemanticFallbackTopK is 0
// or negative. The fallback should still fire with at least one
// result rather than asking the provider for zero rows.
func TestTrySemanticFallback_TopKClampsBelowOne(t *testing.T) {
	var seenLimit int
	provider := &mockSemanticProvider{
		searchTablesFunc: func(_ context.Context, f semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			seenLimit = f.Limit
			return []semantic.TableSearchResult{{URN: fallbackTestURN, Name: "x"}}, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: true, SemanticFallbackTopK: 0},
	}
	_ = enricher.trySemanticFallback(context.Background(), semantic.TableIdentifier{Table: fallbackTestTable})
	if seenLimit != 1 {
		t.Errorf("filter.Limit = %d; want 1 (clamped)", seenLimit)
	}
}

// TestTrySemanticFallback_ProviderError swallows the error per
// the helper's contract: a failed similarity search must not
// surface as an enrichment error, just as a URN miss does not.
func TestTrySemanticFallback_ProviderError(t *testing.T) {
	provider := &mockSemanticProvider{
		searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			return nil, errors.New("upstream search 500")
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: true, SemanticFallbackTopK: 1},
	}
	got := enricher.trySemanticFallback(context.Background(), semantic.TableIdentifier{Table: fallbackTestTable})
	if got != nil {
		t.Errorf("got %v; want nil when search errors", got)
	}
}

// TestTrySemanticFallback_EmptyQuerySkipsSearch confirms that an
// empty table identifier short-circuits before any provider call.
// Without this, a malformed request could send "" to the upstream
// and bloat its query logs.
func TestTrySemanticFallback_EmptyQuerySkipsSearch(t *testing.T) {
	called := false
	provider := &mockSemanticProvider{
		searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			called = true
			return nil, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: true, SemanticFallbackTopK: 1},
	}
	if got := enricher.trySemanticFallback(context.Background(), semantic.TableIdentifier{}); got != nil {
		t.Errorf("got %v; want nil for empty identifier", got)
	}
	if called {
		t.Error("SearchTables was called for an empty identifier")
	}
}

// TestAppendSemanticFallbackSuggestions_PayloadShape verifies the
// shape of the appended content block: match_kind must equal
// EnrichmentMatchSemantic so the model can distinguish the
// suggestion from URN-resolved enrichment, and each suggested
// match must carry urn+name at minimum.
func TestAppendSemanticFallbackSuggestions_PayloadShape(t *testing.T) {
	result := &mcp.CallToolResult{}
	table := semantic.TableIdentifier{Schema: fallbackTestSchema, Table: fallbackTestTable}
	suggestions := []semantic.TableSearchResult{
		{URN: fallbackTestURN, Name: "orders", Platform: "trino", Description: "Orders facts", Tags: []string{"finance"}, Domain: "Sales"},
	}
	out, err := appendSemanticFallbackSuggestions(result, table, suggestions)
	if err != nil {
		t.Fatalf("appendSemanticFallbackSuggestions: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(out.Content))
	}
	text, ok := out.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("appended content is not TextContent: %T", out.Content[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	fallback, ok := decoded["semantic_fallback"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing semantic_fallback key: %s", text.Text)
	}
	if got := fallback["match_kind"]; got != EnrichmentMatchSemantic {
		t.Errorf("match_kind = %v; want %q", got, EnrichmentMatchSemantic)
	}
	if got := fallback["queried_table"]; got != table.String() {
		t.Errorf("queried_table = %v; want %q", got, table.String())
	}
	matches, ok := fallback["suggested_matches"].([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("suggested_matches malformed: %v", fallback["suggested_matches"])
	}
	first, ok := matches[0].(map[string]any)
	if !ok {
		t.Fatalf("first match is not an object: %T", matches[0])
	}
	if first["urn"] != fallbackTestURN || first["name"] != "orders" {
		t.Errorf("suggested match malformed: %v", first)
	}
}

func TestAppendSemanticFallbackSuggestions_EmptyIsNoOp(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "original"}}}
	out, err := appendSemanticFallbackSuggestions(result, semantic.TableIdentifier{Table: "x"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Content) != 1 {
		t.Errorf("content len = %d; want 1 (no append for empty suggestions)", len(out.Content))
	}
}

// TestEnrichTrinoResult_FallbackOnURNMissSetsMatchKind is the
// end-to-end behavioral test for issue #444: a single-table
// enrichment that misses on URN equality, with the fallback
// enabled, produces a suggested-matches payload AND sets
// pc.EnrichmentMatchKind to EnrichmentMatchSemantic so the audit
// row records the heuristic match.
func TestEnrichTrinoResult_FallbackOnURNMissSetsMatchKind(t *testing.T) {
	provider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return nil, errors.New("entity not found")
		},
		searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			return []semantic.TableSearchResult{{URN: fallbackTestURN, Name: "orders"}}, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: true, SemanticFallbackTopK: 1},
	}
	pc := &PlatformContext{}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "original"}}}
	enriched, err := enricher.enrichTrinoResult(context.Background(), result, trinoDescribeRequest(t, fallbackTestSchema+"."+fallbackTestTable), nil, pc)
	if err != nil {
		t.Fatalf("enrichTrinoResult: %v", err)
	}
	if len(enriched.Content) != 2 {
		t.Fatalf("content len = %d; want 2 (original + suggested matches)", len(enriched.Content))
	}
	if pc.EnrichmentMatchKind != EnrichmentMatchSemantic {
		t.Errorf("pc.EnrichmentMatchKind = %q; want %q", pc.EnrichmentMatchKind, EnrichmentMatchSemantic)
	}
	text, _ := enriched.Content[1].(*mcp.TextContent)
	if !strings.Contains(text.Text, EnrichmentMatchSemantic) {
		t.Errorf("appended content missing match_kind marker: %s", text.Text)
	}
}

// TestEnrichTrinoResult_URNHitSetsMatchKindURN covers the happy
// path of acceptance criterion #5: a successful URN lookup tags
// the audit row with match_kind=urn so operators can later compute
// the URN-vs-semantic ratio.
func TestEnrichTrinoResult_URNHitSetsMatchKindURN(t *testing.T) {
	provider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{URN: fallbackTestURN, Description: "orders facts"}, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{}, // fallback off; URN lookup succeeds
	}
	pc := &PlatformContext{}
	result := &mcp.CallToolResult{}
	_, err := enricher.enrichTrinoResult(context.Background(), result, trinoDescribeRequest(t, fallbackTestSchema+"."+fallbackTestTable), nil, pc)
	if err != nil {
		t.Fatalf("enrichTrinoResult: %v", err)
	}
	if pc.EnrichmentMatchKind != EnrichmentMatchURN {
		t.Errorf("pc.EnrichmentMatchKind = %q; want %q", pc.EnrichmentMatchKind, EnrichmentMatchURN)
	}
}

// TestEnrichTrinoResult_FallbackDisabledLeavesMatchKindEmpty
// guards the audit-row default: when the URN lookup misses and
// the fallback is OFF, no enrichment runs, no payload is
// appended, and match_kind stays empty. Operators querying for
// match_kind=” get the count of "tried to enrich, found nothing,
// fallback opted out" events.
func TestEnrichTrinoResult_FallbackDisabledLeavesMatchKindEmpty(t *testing.T) {
	provider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return nil, errors.New("entity not found")
		},
		searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
			t.Error("SearchTables was called despite fallback being disabled")
			return nil, nil
		},
	}
	enricher := &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{SemanticFallbackEnabled: false},
	}
	pc := &PlatformContext{}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "original"}}}
	enriched, err := enricher.enrichTrinoResult(context.Background(), result, trinoDescribeRequest(t, fallbackTestSchema+"."+fallbackTestTable), nil, pc)
	if err != nil {
		t.Fatalf("enrichTrinoResult: %v", err)
	}
	if len(enriched.Content) != 1 {
		t.Errorf("content len = %d; want 1 (no append when fallback off)", len(enriched.Content))
	}
	if pc.EnrichmentMatchKind != "" {
		t.Errorf("pc.EnrichmentMatchKind = %q; want empty", pc.EnrichmentMatchKind)
	}
}
