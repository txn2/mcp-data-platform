package schemagate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	testConn = "warehouse"
	ordersFQ = "hive.sales.orders"
)

// fakeDescriber serves one connection whose defaults are hive.sales.
type fakeDescriber struct {
	mu     sync.Mutex
	tables map[string][]Column // catalog.schema.table -> columns
	calls  int
}

func (*fakeDescriber) Namespace(connection string) (Namespace, bool) {
	if connection != "" && connection != testConn {
		return Namespace{}, false
	}
	return Namespace{Connection: testConn, Catalog: "hive", Schema: "sales"}, true
}

func (f *fakeDescriber) Describe(_ context.Context, _, catalog, schema, table string) ([]Column, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	cols, ok := f.tables[strings.ToLower(catalog+"."+schema+"."+table)]
	if !ok {
		return nil, errors.New("table not found")
	}
	return append([]Column(nil), cols...), nil
}

func newDescriber() *fakeDescriber {
	return &fakeDescriber{tables: map[string][]Column{
		ordersFQ: {
			{Name: "p_id", Type: "bigint", Comment: "record id"},
			{Name: "id", Type: "bigint"},
			{Name: "amount", Type: "decimal(12,2)"},
		},
	}}
}

// fakeSemantic returns curation for orders' p_id.
type fakeSemantic struct{ semantic.Provider }

func (fakeSemantic) GetColumnsContext(_ context.Context, t semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	if t.Table != "orders" {
		return nil, errors.New("not cataloged")
	}
	return map[string]*semantic.ColumnContext{
		"p_id": {Name: "p_id", Description: "The order's record id", GlossaryTerms: []semantic.GlossaryTerm{{Name: "Order ID"}}},
	}, nil
}

type harness struct {
	t     *testing.T
	gate  *Gate
	desc  *fakeDescriber
	ran   int
	reply func(name string) mcp.Result
	pc    *middleware.PlatformContext
}

func newHarness(t *testing.T, refuse bool) *harness {
	t.Helper()
	h := &harness{t: t, desc: newDescriber()}
	h.gate = New(Config{
		Store:     searchgate.NewMemoryStore(time.Hour),
		Describer: h.desc,
		Semantic:  fakeSemantic{},
		Require:   refuse,
	})
	h.pc = middleware.NewPlatformContext("r1")
	h.pc.UserID, h.pc.AuthType, h.pc.Source = "u1", middleware.AuthTypeAPIKey, middleware.SourceMCP
	h.reply = func(string) mcp.Result {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	}
	return h
}

func (h *harness) call(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(h.t, err)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: raw}}
	handler := h.gate.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		h.ran++
		return h.reply(name), nil
	})
	res, err := handler(middleware.WithPlatformContext(context.Background(), h.pc), "tools/call", req)
	require.NoError(h.t, err)
	cr, ok := res.(*mcp.CallToolResult)
	require.True(h.t, ok)
	return cr
}

func text(cr *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range cr.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_, _ = b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestUnreadTableIsRefusedWithItsSchema(t *testing.T) {
	h := newHarness(t, true)
	res := h.call(toolQuery, map[string]any{"sql": "SELECT amount FROM orders WHERE id = 123"})

	require.True(t, res.IsError)
	assert.Equal(t, 0, h.ran, "the statement must not run")
	body := text(res)
	assert.Contains(t, body, "SCHEMA_REQUIRED")
	assert.Contains(t, body, "Table hive.sales.orders (connection warehouse)")
	assert.Contains(t, body, "- p_id bigint: The order's record id; comment: record id; glossary: Order ID")
	assert.Contains(t, body, "- id bigint")
	sc, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok)
	raw, _ := json.Marshal(sc["error"])
	assert.Contains(t, string(raw), `"code":"schema_required"`)

	// The refusal delivered the schema: the same statement resent runs.
	res = h.call(toolQuery, map[string]any{"sql": "SELECT amount FROM orders WHERE id = 123"})
	assert.False(t, res.IsError)
	assert.Equal(t, 1, h.ran)
}

func TestReadsOpenTheTable(t *testing.T) {
	stmt := map[string]any{"sql": "SELECT p_id FROM hive.sales.orders", "connection": testConn}
	reads := map[string]struct {
		tool string
		args map[string]any
	}{
		"describe":           {toolDescribe, map[string]any{"catalog": "hive", "schema": "sales", "table": "orders"}},
		"describe defaults":  {toolDescribe, map[string]any{"table": "ORDERS", "connection": testConn}},
		"fetch dataset":      {toolFetch, map[string]any{"reference": "mcp:dataset:(trino,warehouse,hive.sales.orders)"}},
		"DESCRIBE statement": {toolQuery, map[string]any{"sql": "DESCRIBE sales.orders"}},
		"SHOW COLUMNS":       {toolQuery, map[string]any{"sql": `show columns from "hive"."sales"."orders"`}},
	}
	for name, r := range reads {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			h.reply = func(n string) mcp.Result {
				if n == toolFetch {
					return &mcp.CallToolResult{StructuredContent: map[string]any{"found": true}}
				}
				return &mcp.CallToolResult{}
			}
			read := h.call(r.tool, r.args)
			require.False(t, read.IsError, text(read))
			res := h.call(toolExecute, stmt)
			assert.False(t, res.IsError, text(res))
			assert.Equal(t, 2, h.ran)
		})
	}
}

func TestFailedReadsDoNotOpenTheTable(t *testing.T) {
	h := newHarness(t, true)
	h.reply = func(n string) mcp.Result {
		if n == toolFetch {
			return &mcp.CallToolResult{StructuredContent: map[string]any{"found": false}}
		}
		return &mcp.CallToolResult{IsError: true}
	}
	h.call(toolDescribe, map[string]any{"table": "orders"})
	h.call(toolFetch, map[string]any{"reference": "mcp:dataset:(trino,warehouse,hive.sales.orders)"})
	h.call(toolFetch, map[string]any{"reference": "mcp:dataset:(api,crm,contacts)"})
	h.reply = func(string) mcp.Result { return &mcp.CallToolResult{} }
	res := h.call(toolQuery, map[string]any{"sql": "SELECT * FROM orders"})
	assert.True(t, res.IsError)
}

func TestStatementsTheGateLeavesAlone(t *testing.T) {
	cases := map[string]map[string]any{
		"no tables":            {"sql": "SELECT 1"},
		"metadata tables":      {"sql": "SELECT * FROM hive.information_schema.columns JOIN system.runtime.nodes ON true"},
		"unknown table":        {"sql": "SELECT * FROM missing_table"},
		"unknown connection":   {"sql": "SELECT * FROM orders", "connection": "elsewhere"},
		"empty statement":      {"sql": ""},
		"cte only":             {"sql": "WITH x AS (SELECT 1 AS a) SELECT a FROM x"},
		"show tables":          {"sql": "SHOW TABLES FROM orders"},
		"table function":       {"sql": "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', index => 'logs', query => '{}'))"},
		"malformed arguments ": nil,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			res := h.call(toolQuery, args)
			assert.False(t, res.IsError, text(res))
			assert.Equal(t, 1, h.ran)
		})
	}
}

func TestOtherToolsAndSourcesAreNeverRefused(t *testing.T) {
	h := newHarness(t, true)
	for _, tool := range []string{"api_invoke_endpoint", "graphql_query", "s3_list", "search"} {
		res := h.call(tool, map[string]any{"sql": "SELECT * FROM orders"})
		assert.False(t, res.IsError, tool)
	}
	for _, src := range []string{middleware.SourceScript, middleware.SourceAdmin, middleware.SourceREST} {
		h.pc.Source = src
		res := h.call(toolQuery, map[string]any{"sql": "SELECT * FROM orders"})
		assert.False(t, res.IsError, src)
	}
	assert.Equal(t, 7, h.ran)
}

func TestRequireOffRefusesNothing(t *testing.T) {
	h := newHarness(t, false)
	res := h.call(toolExport, map[string]any{"sql": "SELECT * FROM orders"})
	assert.False(t, res.IsError)
	assert.Equal(t, 1, h.ran)
}

func trinoFailure(errorName string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "query failed"}},
		StructuredContent: map[string]any{"error": map[string]any{
			"code": "trino_query_failed", "category": "client_input",
			"trino": map[string]any{"error_name": errorName, "error_type": "USER_ERROR"},
		}},
	}
}

func TestSchemaFailureCarriesTheColumns(t *testing.T) {
	for _, name := range []string{"COLUMN_NOT_FOUND", "TYPE_MISMATCH"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.reply = func(string) mcp.Result { return trinoFailure(name) }
			res := h.call(toolQuery, map[string]any{"sql": "SELECT customer FROM orders"})
			require.True(t, res.IsError)
			body := text(res)
			assert.Contains(t, body, "query failed")
			assert.Contains(t, body, "These are the real columns")
			assert.Contains(t, body, "- amount decimal(12,2)")
		})
	}
}

func TestOtherFailuresAreReturnedAsTheyAre(t *testing.T) {
	failures := map[string]*mcp.CallToolResult{
		"access denied": trinoFailure("PERMISSION_DENIED"),
		"timeout": {
			IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "query failed"}},
			StructuredContent: map[string]any{"error": map[string]any{"code": "trino_query_failed", "transport": map[string]any{"kind": "timeout"}}},
		},
		"unstructured": {IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "query failed"}}},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			h.reply = func(string) mcp.Result { return failure }
			res := h.call(toolQuery, map[string]any{"sql": "SELECT customer FROM orders"})
			assert.Equal(t, "query failed", text(res))
		})
	}
}

func TestScriptFailuresAreNotEnriched(t *testing.T) {
	h := newHarness(t, true)
	h.pc.Source = middleware.SourceScript
	h.reply = func(string) mcp.Result { return trinoFailure("COLUMN_NOT_FOUND") }
	res := h.call(toolQuery, map[string]any{"sql": "SELECT customer FROM orders"})
	assert.Equal(t, "query failed", text(res))
}

func TestStoreReadErrorLetsTheStatementRun(t *testing.T) {
	h := newHarness(t, true)
	h.gate.cfg.Store = failingStore{}
	res := h.call(toolQuery, map[string]any{"sql": "SELECT * FROM orders"})
	assert.False(t, res.IsError)
}

type failingStore struct{ searchgate.Store }

func (failingStore) HasDiscovered(context.Context, string) (bool, error) {
	return false, errors.New("db down")
}

func TestRefusalBoundsWhatItRepeats(t *testing.T) {
	cols := make([]Column, maxColumnsPerTable+5)
	for i := range cols {
		cols[i] = Column{Name: "c", Type: "varchar", Comment: strings.Repeat("x", 1000)}
	}
	text := failureColumnsText([]describedTable{{ref: tableRef{connection: testConn, catalog: "a", schema: "b", table: "c"}, columns: cols}})
	assert.Contains(t, text, "... 5 more columns")
	assert.NotContains(t, text, strings.Repeat("x", maxTextRunes+1))
}

func TestReadsAreScopedPerCaller(t *testing.T) {
	h := newHarness(t, true)
	h.call(toolDescribe, map[string]any{"table": "orders"})
	h.pc.UserID = "u2"
	res := h.call(toolQuery, map[string]any{"sql": "SELECT * FROM orders"})
	assert.True(t, res.IsError, "another caller's read does not open the table")
}

func TestOverlongTableNameIsNotHeldBack(t *testing.T) {
	h := newHarness(t, true)
	long := strings.Repeat("t", maxReadKeyBytes)
	h.desc.tables["hive.sales."+long] = []Column{{Name: "a", Type: "bigint"}}
	res := h.call(toolQuery, map[string]any{"sql": "SELECT a FROM " + long})
	assert.False(t, res.IsError)
}

type failingMarkStore struct{ searchgate.Store }

func (failingMarkStore) HasDiscovered(context.Context, string) (bool, error) { return false, nil }
func (failingMarkStore) MarkDiscovered(context.Context, string) error {
	return errors.New("db read-only")
}

func TestFailedReadWriteStillRefusesWithTheSchema(t *testing.T) {
	h := newHarness(t, true)
	h.gate.cfg.Store = failingMarkStore{}
	res := h.call(toolQuery, map[string]any{"sql": "SELECT * FROM orders"})
	assert.True(t, res.IsError)
	assert.Contains(t, text(res), "- p_id bigint")
}

func TestParseCallRejectsWhatIsNotAToolCall(t *testing.T) {
	_, ok := parseCall(nil)
	assert.False(t, ok)
	_, ok = parseCall(&mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}})
	assert.False(t, ok)
}

func TestMiddlewarePassesThroughOtherMethodsAndCallers(t *testing.T) {
	h := newHarness(t, true)
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		h.ran++
		return &mcp.ListToolsResult{}, nil
	}
	mw := h.gate.Middleware()(next)
	_, err := mw(context.Background(), "tools/list", &mcp.ListToolsRequest{})
	require.NoError(t, err)
	// No PlatformContext, and a context whose caller has no scope.
	raw, _ := json.Marshal(map[string]any{"sql": "SELECT * FROM orders"})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: toolQuery, Arguments: raw}}
	_, err = mw(context.Background(), "tools/call", req)
	require.NoError(t, err)
	_, err = mw(middleware.WithPlatformContext(context.Background(), middleware.NewPlatformContext("r")), "tools/call", req)
	require.NoError(t, err)
	assert.Equal(t, 3, h.ran)
}

func TestAHandlerErrorIsNotARead(t *testing.T) {
	assert.False(t, succeeded(nil, errors.New("transport")))
	assert.False(t, succeeded(&mcp.ListToolsResult{}, nil))
}

func TestFailureWithUnreadableTablesIsLeftAlone(t *testing.T) {
	h := newHarness(t, false)
	h.reply = func(string) mcp.Result { return trinoFailure("COLUMN_NOT_FOUND") }
	res := h.call(toolQuery, map[string]any{"sql": "SELECT x FROM missing_table"})
	assert.Equal(t, "query failed", text(res))
}

func TestDatasetTableReadsOnlyTrinoTables(t *testing.T) {
	for _, ref := range []string{"not a reference", "mcp:dataset:(trino,warehouse,orders)", "mcp:tag:pii"} {
		_, ok := datasetTable(ref)
		assert.False(t, ok, ref)
	}
	got, ok := datasetTable("mcp:dataset:(trino,warehouse,hive.sales.orders)")
	require.True(t, ok)
	assert.Equal(t, "hive.sales.orders", got.fullName())
}
