package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/sqltables"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// chanCaptor forwards each correction to a channel for deterministic assertions
// against the middleware's asynchronous dispatch.
type chanCaptor struct{ ch chan CorrectionCapture }

func (c chanCaptor) CaptureCorrection(_ context.Context, cc CorrectionCapture) error {
	c.ch <- cc
	return nil
}

func mkQueryReq(args string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "trino_query", Arguments: json.RawMessage(args)}}
}

func bareErrResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func okResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
}

func queryPC() *PlatformContext {
	pc := NewPlatformContext("req-1")
	pc.SessionID = "s1"
	pc.UserEmail = "analyst@example.com"
	pc.PersonaName = "analyst"
	pc.ToolName = "trino_query"
	// A tool call carries the kind of the toolkit that served it; the URN
	// builder reads it to name the platform a table belongs to (#1384).
	pc.ToolkitKind = "trino"
	return pc
}

func TestReflexiveObserve_ErrorThenFixDispatches(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()
	ch := make(chan CorrectionCapture, 1)
	cfg := ReflexiveCaptureConfig{
		Captor:     chanCaptor{ch: ch},
		Tracker:    tr,
		URNBuilder: func(k, _, c, s, tb string) string { return "urn:" + k + ":" + c + "." + s + "." + tb },
	}
	pc := queryPC()

	cfg.observe(mkQueryReq(`{"sql":"SELECT custmer_id FROM cat.sch.orders","connection":"primary"}`),
		bareErrResult("Column 'custmer_id' cannot be resolved"), pc)
	cfg.observe(mkQueryReq(`{"sql":"SELECT customer_id FROM cat.sch.orders","connection":"primary"}`),
		okResult(), pc)

	select {
	case cc := <-ch:
		if cc.SinkClass != "schema_entity" || cc.Category != "correction" {
			t.Errorf("sink/category = %q/%q", cc.SinkClass, cc.Category)
		}
		if cc.CreatedBy != "analyst@example.com" {
			t.Errorf("CreatedBy = %q", cc.CreatedBy)
		}
		if len(cc.EntityURNs) != 1 || cc.EntityURNs[0] != "urn:trino:cat.sch.orders" {
			t.Errorf("EntityURNs = %v", cc.EntityURNs)
		}
		if !strings.Contains(cc.Content, "custmer_id") || !strings.Contains(cc.Content, "customer_id") {
			t.Errorf("content missing failed/fixed SQL:\n%s", cc.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatched correction")
	}
}

func TestReflexiveObserve_EarlyReturns(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()
	cfg := ReflexiveCaptureConfig{Captor: chanCaptor{ch: make(chan CorrectionCapture, 1)}, Tracker: tr}
	pc := queryPC()

	// No sql argument: nothing recorded.
	cfg.observe(mkQueryReq(`{}`), bareErrResult("Column x cannot be resolved"), pc)
	// A query with no physical table: nothing recorded.
	cfg.observe(mkQueryReq(`{"sql":"SELECT 1"}`), bareErrResult("Column x cannot be resolved"), pc)
	// A non-CallToolResult result is ignored.
	cfg.observe(mkQueryReq(`{"sql":"SELECT a FROM cat.sch.t"}`), &mcp.ListToolsResult{}, pc)

	if tr.SessionCount() != 0 {
		t.Errorf("no failures should have been recorded, sessions=%d", tr.SessionCount())
	}
}

func TestReflexiveRecordFailure_NoiseSkipped(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()
	cfg := ReflexiveCaptureConfig{Tracker: tr}

	cfg.recordFailure("s1", "SELECT bad_col FROM cat.sch.orders", "primary",
		bareErrResult("dial tcp: connection refused"))
	if tr.SessionCount() != 0 {
		t.Errorf("infra-noise error must not be recorded, sessions=%d", tr.SessionCount())
	}

	cfg.recordFailure("s1", "SELECT bad_col FROM cat.sch.orders", "primary",
		bareErrResult("Column 'bad_col' cannot be resolved"))
	fix := "SELECT good_col FROM cat.sch.orders"
	if got := tr.TakeResolved("s1", "primary", meaningfulIdentifiers(fix), normalizeSQLText(fix)); got == nil {
		t.Error("worthy error should be recorded and resolvable")
	}
}

func TestErrorMessageFromResult(t *testing.T) {
	// Structured error (GetError populated) is preferred.
	structured := NewToolResultError("Table 'x' does not exist")
	if got := errorMessageFromResult(structured); got != "Table 'x' does not exist" {
		t.Errorf("structured message = %q", got)
	}
	// Bare IsError result falls back to the first text content.
	bare := bareErrResult("Column 'y' cannot be resolved")
	if got := errorMessageFromResult(bare); got != "Column 'y' cannot be resolved" {
		t.Errorf("bare message = %q", got)
	}
}

func TestSQLAndConnectionFromRequest(t *testing.T) {
	if sql, conn := sqlAndConnectionFromRequest(nil); sql != "" || conn != "" {
		t.Errorf("nil request should yield empty, got %q/%q", sql, conn)
	}
	sql, conn := sqlAndConnectionFromRequest(mkQueryReq(`{"sql":"SELECT 1","connection":"primary"}`))
	if sql != "SELECT 1" || conn != "primary" {
		t.Errorf("got %q/%q, want SELECT 1/primary", sql, conn)
	}
}

func TestWorthCapturingQueryError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"column not resolved", "line 1:8: Column 'custmer_id' cannot be resolved", true},
		{"table not found", "line 1:15: Table 'hive.sales.ordrs' does not exist", true},
		{"function not registered", "Function 'dateadd' not registered", true},
		{"type mismatch operator", "'=' cannot be applied to integer, varchar", true},
		{"ambiguous column", "Column 'id' is ambiguous", true},
		{"wrong arity", "Unexpected parameters (varchar) for function lower", true},
		{"group by misconception", "'amount' must be an aggregate expression or appear in GROUP BY clause", true},
		{"explicit type mismatch", "Type mismatch between bigint and varchar", true},
		{"uppercase still matches", "COLUMN 'X' CANNOT BE RESOLVED", true},

		{"access denied vetoed", "Access Denied: cannot select from table that does not exist", false},
		{"permission denied vetoed", "permission denied for schema sales", false},
		{"connection refused is noise", "dial tcp 10.0.0.1:8080: connection refused", false},
		{"deadline is noise", "context deadline exceeded", false},
		{"generic failure is noise", "Query failed: internal error", false},
		{"empty is noise", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worthCapturingQueryError(tt.msg); got != tt.want {
				t.Errorf("worthCapturingQueryError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestNormalizeSQLText(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"SELECT   *\n FROM  Orders", "select * from orders"},
		{"select * from orders", "select * from orders"},
		{"  SELECT 1  ", "select 1"},
	}
	for _, tt := range tests {
		if got := normalizeSQLText(tt.in); got != tt.want {
			t.Errorf("normalizeSQLText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Formatting-only differences normalize to equal; a real edit does not.
	if normalizeSQLText("SELECT a FROM t") != normalizeSQLText("select  a\nfrom t") {
		t.Error("formatting-only variants should normalize equal")
	}
	if normalizeSQLText("SELECT a FROM t") == normalizeSQLText("SELECT b FROM t") {
		t.Error("distinct statements should not normalize equal")
	}
}

func TestMeaningfulIdentifiers(t *testing.T) {
	ids := meaningfulIdentifiers("SELECT customer_id, amount FROM sales.orders WHERE region = 'x'")
	// Keywords (select, from, where) are stripped; schema identifiers remain.
	for _, kw := range []string{"select", "from", "where"} {
		if _, ok := ids[kw]; ok {
			t.Errorf("keyword %q should be stripped", kw)
		}
	}
	for _, id := range []string{"customer_id", "amount", "sales", "orders", "region"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("identifier %q should be present", id)
		}
	}
}

func TestBuildCorrectionContent(t *testing.T) {
	failed := FailedQuery{
		RawSQL:       "SELECT custmer_id FROM sales.orders",
		ErrorMessage: "Column 'custmer_id' cannot be resolved",
	}
	content := buildCorrectionContent(failed, "SELECT customer_id FROM sales.orders")
	for _, want := range []string{"custmer_id", "customer_id", "cannot be resolved", "Failed query", "Corrected query"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q:\n%s", want, content)
		}
	}
	// Comfortably clears the memory content minimum (10 bytes).
	if len(content) < 50 {
		t.Errorf("content unexpectedly short: %d bytes", len(content))
	}
}

func TestTruncateForCapture(t *testing.T) {
	if got := truncateForCapture("  short  ", 100); got != "short" {
		t.Errorf("truncateForCapture trims and returns short string, got %q", got)
	}
	long := strings.Repeat("x", 50)
	got := truncateForCapture(long, 10)
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Errorf("expected first 10 chars retained, got %q", got)
	}

	// A multi-byte rune straddling the cut must not be split (would be invalid
	// UTF-8 and make the Postgres INSERT fail, silently dropping the capture).
	multibyte := strings.Repeat("é", 50) // each 'é' is 2 bytes
	out := truncateForCapture(multibyte, 11)
	if !utf8.ValidString(out) {
		t.Errorf("truncation produced invalid UTF-8: %q", out)
	}
}

func TestReflexiveEntityURNs(t *testing.T) {
	cfg := ReflexiveCaptureConfig{
		URNBuilder: func(kind, _, catalog, schema, table string) string {
			return "urn:" + kind + ":" + catalog + "." + schema + "." + table
		},
	}
	refs := sqltables.Extract("SELECT * FROM cat.sch.orders o JOIN cat.sch.customers c ON o.id = c.id")
	urns := cfg.entityURNs("trino", "primary", refs)
	if len(urns) != 2 {
		t.Fatalf("expected 2 urns, got %v", urns)
	}

	// A two-part table (no catalog) yields no URN; a nil builder yields none.
	partial := sqltables.Extract("SELECT * FROM sch.orders")
	if got := cfg.entityURNs("trino", "primary", partial); got != nil {
		t.Errorf("two-part table should not produce a URN, got %v", got)
	}
	nilCfg := ReflexiveCaptureConfig{}
	if got := nilCfg.entityURNs("trino", "primary", refs); got != nil {
		t.Errorf("nil builder should produce no URNs, got %v", got)
	}
}

func TestIsReflexiveQueryTool(t *testing.T) {
	if !isReflexiveQueryTool("trino_query") || !isReflexiveQueryTool("trino_execute") {
		t.Error("trino query tools should be observed")
	}
	if isReflexiveQueryTool("datahub_get_entity") || isReflexiveQueryTool("search") {
		t.Error("non-query tools should not be observed")
	}
}
