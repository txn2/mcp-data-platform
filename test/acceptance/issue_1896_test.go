//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Issue #1896: the platform's own use of Trino, object storage and PostgreSQL
// is measured. A trino_query is counted by statement type with a trino.* span
// under its tool call that carries no SQL text; a portal asset save is counted
// as a put of the portal's assets with the bytes it stored; a search's
// pgvector ranking is a span of its own in the search's trace.
//
// Wire forms: trino_query's connection, sql and purpose, save_asset's name,
// content, content_type and description, and search's intent and purpose are
// typed strings, so each admits one JSON form and is sent as a literal
// tools/call parameter of it.
//
// Stack: `make dev`, whose own Trino (:9283) is behind the scratch connection,
// whose OTLP collector the #1892 criteria read spans from, and whose Ollama
// embeds the search query so the search ranks by vector.

const issue1896Purpose = "Acceptance #1896: the platform's own dependency calls are measured."

// issue1896Literal is a value the statement carries; it must reach no label
// and no span attribute.
const issue1896Literal = "acc-1896-card-4111"

// spansInTrace waits until the trace holds a span the predicate accepts, and
// returns every span of the trace read at that moment.
func spansInTrace(t *testing.T, traceID string, want func(exportedSpan) bool) []exportedSpan {
	t.Helper()
	deadline := time.Now().Add(spanWait)
	for {
		var in []exportedSpan
		found := false
		for _, sp := range readSpans(t) {
			if sp.TraceID != traceID {
				continue
			}
			in = append(in, sp)
			if want(sp) {
				found = true
			}
		}
		if found {
			return in
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s held no span the criterion looks for within %s (%d spans)", traceID, spanWait, len(in))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1896_ATrinoQueryIsCountedAndTracedByStatementType: trino_query
// through the real client increments trino_queries_total{query_kind="select"}
// and produces a trino.select child of the tools/call span whose attributes
// carry the statement type and no SQL text.
func TestIssue1896_ATrinoQueryIsCountedAndTracedByStatementType(t *testing.T) {
	c := connect(t)
	labels := map[string]string{"query_kind": "select", "status": "ok"}
	before := metricSeries(scrapeRaw(t), "trino_queries_total", labels)
	c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"purpose":    issue1896Purpose,
		"sql":        "SELECT '" + issue1896Literal + "' AS v",
	})
	after := metricSeries(scrapeRaw(t), "trino_queries_total", labels)
	if after <= before {
		t.Fatalf("trino_queries_total{query_kind=select,status=ok} did not increase (%v -> %v)", before, after)
	}
	if strings.Contains(scrapeRaw(t), issue1896Literal) {
		t.Error("the statement's literal reached /metrics")
	}

	tool := awaitSpan(t, c.sessionID, "trino_query")
	spans := spansInTrace(t, tool.TraceID, func(sp exportedSpan) bool {
		return sp.Name == "trino.select" && sp.ParentSpanID == tool.SpanID
	})
	for _, sp := range spans {
		if sp.Name != "trino.select" {
			continue
		}
		if sp.Attrs["db.operation.name"] != "SELECT" || sp.Attrs["db.system.name"] != "trino" {
			t.Errorf("trino.select attributes = %v, want db.operation.name=SELECT and db.system.name=trino", sp.Attrs)
		}
		for k, v := range sp.Attrs {
			if strings.Contains(v, issue1896Literal) {
				t.Errorf("attribute %s carries the statement's text: %q", k, v)
			}
		}
	}
}

// TestIssue1896_APortalAssetSaveIsCountedAsAPut: a save_asset increments
// storage_operations_total{purpose="portal_assets",operation="put"} and
// storage_bytes_written_total{purpose="portal_assets"}.
func TestIssue1896_APortalAssetSaveIsCountedAsAPut(t *testing.T) {
	c := connect(t)
	puts := map[string]string{"purpose": "portal_assets", "operation": "put", "result": "ok"}
	written := map[string]string{"purpose": "portal_assets"}
	body := scrapeRaw(t)
	putsBefore := metricSeries(body, "storage_operations_total", puts)
	bytesBefore := metricSeries(body, "storage_bytes_written_total", written)

	content := "# Acceptance #1896\n\nA portal asset whose write is measured.\n"
	saved := c.call("save_asset", map[string]any{
		"name":         "acc-1896-" + time.Now().UTC().Format("20060102T150405.000"),
		"content":      content,
		"content_type": "text/markdown",
		"description":  issue1896Purpose,
	})
	if id, _ := saved["asset_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
		})
	}

	body = scrapeRaw(t)
	if got := metricSeries(body, "storage_operations_total", puts); got <= putsBefore {
		t.Errorf("storage_operations_total{purpose=portal_assets,operation=put} did not increase (%v -> %v)", putsBefore, got)
	}
	if got := metricSeries(body, "storage_bytes_written_total", written); got-bytesBefore < float64(len(content)) {
		t.Errorf("storage_bytes_written_total{purpose=portal_assets} grew by %v, want at least the %d bytes saved", got-bytesBefore, len(content))
	}
}

// TestIssue1896_ASearchRanksUnderAVectorSearchSpan: a search call produces a
// postgres.vector_search span in the trace of its tools/call span, and the
// statement histogram counts it under operation="vector_search".
func TestIssue1896_ASearchRanksUnderAVectorSearchSpan(t *testing.T) {
	c := connect(t)
	labels := map[string]string{"operation": "vector_search"}
	before := metricSeries(scrapeRaw(t), "db_client_operation_duration_seconds_count", labels)
	c.call("search", map[string]any{
		"intent":  "customer orders by region for the quarterly review",
		"purpose": issue1896Purpose,
	})
	tool := awaitSpan(t, c.sessionID, "search")
	spans := spansInTrace(t, tool.TraceID, func(sp exportedSpan) bool { return sp.Name == "postgres.vector_search" })
	for _, sp := range spans {
		if sp.Name == "postgres.vector_search" {
			if _, ok := sp.Attrs["db.query.text"]; ok {
				t.Error("the vector search span carries the SQL text without the opt-in")
			}
		}
	}
	if after := metricSeries(scrapeRaw(t), "db_client_operation_duration_seconds_count", labels); after <= before {
		t.Errorf("db_client_operation_duration_seconds_count{operation=vector_search} did not increase (%v -> %v)", before, after)
	}
}
