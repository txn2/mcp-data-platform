package middleware_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
)

// These tests exercise the third delivery surface of a checkable insight
// (#1220): the memory_context enrichment block, through the real assembled
// middleware chain (tool-call middleware outer, enrichment middleware inner) on
// a real mcp.Server driven over an in-memory transport. The memory provider and
// the query provider are fakes; everything between them is production code.
//
// What must hold: a pushed insight whose entity resolves to an available table
// arrives naming that table, a pushed plain memory never does (a note is not a
// claim about the warehouse), and an unresolvable entity or an absent verifier
// leaves the block exactly as it was.

const (
	pushURN     = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)"
	pushGoneURN = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.gone,PROD)"
	pushTable   = "catalog.schema.orders"
	pushConn    = "prod-trino"
)

// pushQueryProvider reports one table as available and nothing else.
type pushQueryProvider struct {
	query.NoopProvider
}

func (*pushQueryProvider) GetTableAvailability(_ context.Context, urn string) (*query.TableAvailability, error) {
	if urn != pushURN {
		return &query.TableAvailability{Available: false, Error: "not found"}, nil
	}
	return &query.TableAvailability{Available: true, QueryTable: pushTable, Connection: pushConn}, nil
}

// pushMemoryProvider pushes a fixed set of records regardless of the query,
// standing in for the entity-keyed recall the real bridge performs.
type pushMemoryProvider struct {
	snippets []middleware.MemorySnippet
}

func (p *pushMemoryProvider) RecallForEntities(
	context.Context, []string, string, int,
) ([]middleware.MemorySnippet, error) {
	return p.snippets, nil
}

// pushRecords is the recalled set every test in this file pushes: a checkable
// insight, an insight about an entity the warehouse cannot see, and a plain
// memory that happens to name the same resolvable entity.
func pushRecords() []middleware.MemorySnippet {
	return []middleware.MemorySnippet{
		{
			ID:         "i-checkable",
			Reference:  "mcp:insight:i-checkable",
			Content:    "The orders table holds 1140 rows.",
			Dimension:  "knowledge",
			EntityURNs: []string{pushURN},
			Insight:    true,
		},
		{
			ID:         "i-unresolvable",
			Reference:  "mcp:insight:i-unresolvable",
			Content:    "The retired orders extract holds 1140 rows.",
			Dimension:  "knowledge",
			EntityURNs: []string{pushGoneURN},
			Insight:    true,
		},
		{
			ID:         "m-plain",
			Reference:  "mcp:memory:m-plain",
			Content:    "I prefer results grouped by region.",
			Dimension:  "preference",
			EntityURNs: []string{pushURN},
		},
	}
}

// pushedRecords drives one tool call through the assembled chain and returns the
// memory_context records the enrichment appended, keyed by record id.
//
// The resolver is NOT handed in: the middleware constructor builds it from the
// query provider it is given, which is the only wiring production uses, so the
// test supplies exactly what an operator supplies — a query provider and the
// toggle.
func pushedRecords(t *testing.T, queryProvider query.Provider, enabled bool) map[string]map[string]any {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        chainTestDescribeTable,
		Description: "Describe a table",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"table":{"type":"string"}}}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// The result names the entity the enrichment recalls against.
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: `{"urn":"` + pushURN + `"}`},
		}}, nil
	})

	// Middleware order (innermost first): enrichment, then auth (outermost), so
	// the enrichment reads the PlatformContext the tool-call middleware wrote.
	server.AddReceivingMiddleware(middleware.MCPSemanticEnrichmentMiddleware(
		nil, queryProvider, nil,
		middleware.EnrichmentConfig{VerifiableInsights: enabled},
		&pushMemoryProvider{snippets: pushRecords()},
	))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&testAuthenticator{userInfo: &middleware.UserInfo{UserID: chainTestUser, Roles: []string{chainTestAnalyst}}},
		&testAuthorizer{persona: chainTestAnalyst},
		&testToolkitLookup{tools: map[string]struct{ kind, name, conn string }{
			chainTestDescribeTable: {kind: chainTestTrino, name: chainTestProd, conn: chainTestProdTrino},
		}},
		middleware.ToolCallConfig{Transport: chainTestStdio, AdminPersona: "admin"},
	))

	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf(chainTestConnecting, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      chainTestDescribeTable,
		Arguments: map[string]any{"table": "orders"},
	})
	if err != nil {
		t.Fatalf(chainTestCallingTool, err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	block := assertContentContainsKey(t, result, "memory_context")
	records, ok := block["memory_context"].([]any)
	if !ok {
		t.Fatalf("memory_context is %T, want a list", block["memory_context"])
	}

	byID := make(map[string]map[string]any, len(records))
	for _, r := range records {
		rec, isMap := r.(map[string]any)
		if !isMap {
			t.Fatalf("memory_context entry is %T, want an object", r)
		}
		id, _ := rec["id"].(string)
		byID[id] = rec
	}
	return byID
}

// verifiableOf reads a rendered record's verification marker, failing when the
// record is absent entirely (which would make an assertion about its marker
// vacuous).
func verifiableOf(t *testing.T, records map[string]map[string]any, id string) map[string]any {
	t.Helper()

	rec, ok := records[id]
	if !ok {
		t.Fatalf("record %q was not delivered at all; got %v", id, records)
	}
	v, _ := rec["verifiable"].(map[string]any)
	return v
}

func TestMemoryContext_InsightNamesTheQueryableTable(t *testing.T) {
	records := pushedRecords(t, &pushQueryProvider{}, true)

	v := verifiableOf(t, records, "i-checkable")
	if v == nil {
		t.Fatal("a pushed insight whose entity resolves must name the table that settles its claim")
	}
	if got := v["query_table"]; got != pushTable {
		t.Errorf("query_table = %v, want %q", got, pushTable)
	}
	if got := v["connection"]; got != pushConn {
		t.Errorf("connection = %v, want %q", got, pushConn)
	}
	if got := v["urn"]; got != pushURN {
		t.Errorf("urn = %v, want %q", got, pushURN)
	}

	if v := verifiableOf(t, records, "i-unresolvable"); v != nil {
		t.Errorf("an insight whose entity does not resolve must be unchanged, got %v", v)
	}
	// A plain memory is a note, not a claim about the warehouse, so it carries no
	// marker even though it names the same resolvable entity.
	if v := verifiableOf(t, records, "m-plain"); v != nil {
		t.Errorf("a plain memory record must be unchanged, got %v", v)
	}
}

func TestMemoryContext_ToggleOffLeavesRecordsUnchanged(t *testing.T) {
	records := pushedRecords(t, &pushQueryProvider{}, false)

	for id := range records {
		if v := verifiableOf(t, records, id); v != nil {
			t.Errorf("record %s carries a marker with the toggle off: %v", id, v)
		}
	}
}

func TestMemoryContext_NoopProviderResolvesNothing(t *testing.T) {
	records := pushedRecords(t, query.NewNoopProvider(), true)

	if v := verifiableOf(t, records, "i-checkable"); v != nil {
		t.Errorf("a noop provider cannot support a marker, got %v", v)
	}
}
