package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// describedTable is what a describe resolves to after the connection's catalog
// mapping has been applied.
func describedTable() semantic.TableIdentifier {
	return semantic.TableIdentifier{Catalog: "warehouse", Schema: "sales", Table: "orders"}
}

func provenResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}
}

func TestProvenQueriesAreAppendedToADescribe(t *testing.T) {
	t.Parallel()

	var gotURN, gotUser string
	enricher := &semanticEnricher{cfg: EnrichmentConfig{
		ForConnection: func(string) (string, map[string]string) { return "trino", nil },
		ProvenQueries: func(_ context.Context, urn, userID string, _ int) []ProvenQuery {
			gotURN, gotUser = urn, userID
			return []ProvenQuery{{
				Reference: "mcp:call:evt-1", Purpose: "Revenue by region.",
				Statement: "SELECT region FROM warehouse.sales.orders",
				Outcome:   "satisfied", SatisfiedBy: "capture", ReuseCount: 2,
			}}
		},
	}}

	result := enricher.appendProvenQueries(context.Background(), provenResult(),
		describedTable(), &PlatformContext{UserID: "u1", Connection: "acme"})

	// The lookup is keyed on the dataset the catalog knows the table by, which
	// is the same key a recorded call stores its targets under.
	if gotURN != "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.sales.orders,PROD)" {
		t.Errorf("urn = %q", gotURN)
	}
	if gotUser != "u1" {
		t.Errorf("user = %q, want the caller: a proven query is the caller's own", gotUser)
	}

	text := renderedText(result)
	for _, want := range []string{"proven_queries", "mcp:call:evt-1", "satisfied", "reuse_count"} {
		if !strings.Contains(text, want) {
			t.Errorf("result %q is missing %q", text, want)
		}
	}
	if result.StructuredContent == nil {
		t.Error("the block must be mirrored into structured output, as every other enrichment is")
	}
}

func TestProvenQueriesAreSilentWhenThereAreNone(t *testing.T) {
	t.Parallel()

	enricher := &semanticEnricher{cfg: EnrichmentConfig{
		ProvenQueries: func(context.Context, string, string, int) []ProvenQuery { return nil },
	}}

	result := enricher.appendProvenQueries(context.Background(), provenResult(),
		describedTable(), &PlatformContext{UserID: "u1"})

	// An agent describing a table nobody has queried yet should see nothing
	// about it rather than an empty block.
	if strings.Contains(renderedText(result), "proven_queries") {
		t.Error("an empty result must append nothing")
	}
}

func TestProvenQueriesNeedACallerAndALister(t *testing.T) {
	t.Parallel()

	called := false
	lister := func(context.Context, string, string, int) []ProvenQuery {
		called = true
		return []ProvenQuery{{Reference: "mcp:call:evt-1"}}
	}

	// No lister wired, no caller, and a table the URN grammar cannot name are
	// each reasons to append nothing.
	for _, tc := range []struct {
		name string
		cfg  EnrichmentConfig
		pc   *PlatformContext
		tbl  semantic.TableIdentifier
	}{
		{"no lister", EnrichmentConfig{}, &PlatformContext{UserID: "u1"}, describedTable()},
		{"anonymous", EnrichmentConfig{ProvenQueries: lister}, &PlatformContext{}, describedTable()},
		{"no platform context", EnrichmentConfig{ProvenQueries: lister}, nil, describedTable()},
		{"unnamed table", EnrichmentConfig{ProvenQueries: lister}, &PlatformContext{UserID: "u1"}, semantic.TableIdentifier{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enricher := &semanticEnricher{cfg: tc.cfg}
			result := enricher.appendProvenQueries(context.Background(), provenResult(), tc.tbl, tc.pc)
			if strings.Contains(renderedText(result), "proven_queries") {
				t.Error("nothing should have been appended")
			}
		})
	}
	if called {
		t.Error("the catalog must not be asked when there is nothing to ask about")
	}
}

// renderedText joins a result's text content, which is where an enrichment
// block lands.
func renderedText(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
