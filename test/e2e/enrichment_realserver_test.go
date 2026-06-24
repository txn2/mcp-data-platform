//go:build integration

package e2e

// End-to-end enrichment + documented-parameter assertions through the REAL
// assembled server. Unlike cross_enrichment_test.go (which mocks the tool result
// and exercises only the enrichment middleware), this drives an in-process MCP
// session against the platform's own mcp.Server, so the real trino_describe_table
// / datahub_search handlers run the real query -> URN -> enrichment path the way
// a deployed client does.
//
// The include_sample parameter assertion needs only Trino (make e2e-up). The
// semantic/query enrichment assertions need DataHub seeded with
// test/e2e/testdata/datahub (make e2e-seed) and skip cleanly when DataHub is
// not reachable.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

// The seeded e2e fixture: Trino table memory.e2e_test.test_orders, mirrored in
// DataHub (test/e2e/testdata/datahub/datasets.json) with owners alice/bob and
// the ecommerce tag/domain.
const (
	enrichCatalog = "memory"
	enrichSchema  = "e2e_test"
	enrichTable   = "test_orders"
)

func TestEnrichment_RealAssembledServer(t *testing.T) {
	cfg := helpers.DefaultE2EConfig()
	ctx, cancel := helpers.TestContext(cfg.Timeout)
	defer cancel()

	tp, err := helpers.NewTestPlatform(ctx, cfg)
	require.NoError(t, err, "build assembled platform")
	defer func() { _ = tp.Close() }()

	// Start registers every toolkit's tools (and the platform-level tools) on
	// the MCP server and runs the lifecycle callbacks — the step a deployment
	// performs before serving. Without it the server has no tools to call.
	require.NoError(t, tp.Platform.Start(ctx), "start assembled platform")

	session, err := helpers.ConnectInMemory(ctx, tp.MCPServer())
	require.NoError(t, err, "connect in-process MCP session to the assembled server")
	defer func() { _ = session.Close() }()

	// Documented-parameter effect: include_sample is Trino-only, so this runs
	// with just make e2e-up and proves the real handler executed through the
	// full middleware chain (not a mock result).
	t.Run("include_sample parameter has an observable effect", func(t *testing.T) {
		with := describeTable(t, ctx, session, true)
		sample, _ := with["sample"].([]any)
		assert.NotEmpty(t, sample, "include_sample=true must return sample rows for a populated table")

		without := describeTable(t, ctx, session, false)
		emptySample, _ := without["sample"].([]any)
		assert.Empty(t, emptySample, "include_sample=false must omit sample rows")
	})

	// Trino -> DataHub: the describe result must carry the seeded semantic
	// context (owners/tags) when DataHub is up.
	t.Run("trino_describe_table is enriched with semantic context", func(t *testing.T) {
		if helpers.SkipIfDataHubUnavailable(cfg) {
			t.Skip("DataHub not reachable; run `datahub docker quickstart` + `make e2e-seed` for enrichment assertions")
		}
		res := callTool(t, ctx, session, "trino_describe_table", map[string]any{
			"catalog": enrichCatalog, "schema": enrichSchema, "table": enrichTable,
		})
		sc := helpers.AssertHasSemanticContext(t, res)
		helpers.AssertOwnerPresent(t, sc, "urn:li:corpuser:alice")
		helpers.AssertOwnerPresent(t, sc, "urn:li:corpuser:bob")
		helpers.AssertTagPresent(t, sc, "ecommerce")
	})

	// DataHub -> Trino: a datahub_search result must carry query availability
	// (query_context) for the seeded dataset when DataHub is up.
	t.Run("datahub_search is enriched with query context", func(t *testing.T) {
		if helpers.SkipIfDataHubUnavailable(cfg) {
			t.Skip("DataHub not reachable; run `datahub docker quickstart` + `make e2e-seed` for enrichment assertions")
		}
		res := callTool(t, ctx, session, "datahub_search", map[string]any{
			"query":    "test_orders",
			"platform": "trino",
		})
		helpers.AssertHasQueryContext(t, res)
	})
}

// describeTable calls trino_describe_table through the session and returns the
// parsed structured result (columns, sample, ...).
func describeTable(t *testing.T, ctx context.Context, s *mcp.ClientSession, includeSample bool) map[string]any {
	t.Helper()
	res := callTool(t, ctx, s, "trino_describe_table", map[string]any{
		"catalog":        enrichCatalog,
		"schema":         enrichSchema,
		"table":          enrichTable,
		"include_sample": includeSample,
	})
	return structuredMap(t, res)
}

// structuredMap returns a tool result's structured output as a map. The
// describe tool returns a typed DescribeTableOutput (columns, sample, ...) in
// StructuredContent; the text content is a markdown rendering, so parse the
// structured field for assertions.
func structuredMap(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "tool result has no structured content")
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err, "marshal structured content")
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out), "unmarshal structured content")
	return out
}

// callTool invokes a tool over the in-process session and fails on a transport
// or tool-level error.
func callTool(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "%s transport error", name)
	require.False(t, res.IsError, "%s returned a tool error: %s", name, firstText(res))
	return res
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
