//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1678: a graphql_query whose HTTP 200 carried an errors array
// reported upstream_error on its result and was audited and cataloged as a
// success. The criteria run through the real MCP surface against the real
// GraphQL server the repository's e2e environment brings up (DataHub's GMS at
// /api/graphql), then read the audit row, the call record and the fetched
// record the way an operator and an agent do.
//
// Wire forms: graphql_query's `connection`, `query` and `purpose` are strings
// only, and `variables` is not sent. graphql_export's `name` is a string only.
// No parameter this ticket touches admits a second JSON form.

const issue1678Purpose = "Acceptance for #1678: a GraphQL failure inside a 200 is a failed call wherever the platform records it."

// issue1678BadDocument is a document DataHub's schema admits and its resolver
// refuses: an unparseable URN. warn mode is what lets it reach the endpoint.
const issue1678BadDocument = `query Bad { dataset(urn: "not-a-urn") { urn } }`

// issue1678Call makes one graphql_query and returns the tool's result parsed,
// with the call reference the platform stamped on it.
func issue1678Call(t *testing.T, c *client, tool string, args map[string]any) (out map[string]any, callID string) {
	t.Helper()
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s: the call was refused rather than proxied: %s", tool, text)
	}
	for _, content := range res.Content {
		block, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(block.Text), &parsed) != nil {
			continue
		}
		if ref, ok := parsed["call_reference"].(map[string]any); ok {
			callID, _ = ref["call_id"].(string)
			continue
		}
		if out == nil {
			out = parsed
		}
	}
	if out == nil {
		t.Fatalf("%s answered no JSON block: %s", tool, text)
	}
	if callID == "" {
		t.Fatalf("%s answered without a call reference; the audit row cannot be found without it: %s", tool, text)
	}
	return out, callID
}

// issue1678AuditEvent waits for one call's audit row, which the audit writer
// drains after the call has answered.
func issue1678AuditEvent(t *testing.T, admin *client, callID string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		status, body := admin.rest(http.MethodGet, "/api/v1/admin/audit/events/"+callID, http.NoBody)
		if status == http.StatusOK {
			return body
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no audit event %s within 30s", callID)
	return nil
}

// issue1678CallRecord waits for the catalog record of one call.
func issue1678CallRecord(t *testing.T, admin *client, connection, callID string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		for _, r := range admin.list("/api/v1/admin/calls?connection=" + connection + "&per_page=25") {
			record, _ := r.(map[string]any)
			if id, _ := record["event_id"].(string); id == callID {
				return record
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("call %s on %s was not cataloged within 30s", callID, connection)
	return nil
}

// issue1678Fetch dereferences a call reference through the fetch tool, as an
// agent citing or re-running the call would.
func issue1678Fetch(t *testing.T, c *client, callID string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{"reference": "mcp:call:" + callID, "purpose": issue1678Purpose})
	document, _ := out["document"].(map[string]any)
	content, _ := document["content"].(map[string]any)
	if content == nil {
		t.Fatalf("fetch mcp:call:%s answered without the record: %v", callID, out)
	}
	return content
}

// TestIssue1678_AnErrorsArrayInAnHTTP200IsAuditedAsAFailure is the ticket's
// report: the result says upstream_error and the audit row said success.
func TestIssue1678_AnErrorsArrayInAnHTTP200IsAuditedAsAFailure(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "1678-errors", map[string]any{"schema_validation": "warn"})

	out, callID := issue1678Call(t, c, issue1277QueryTool, map[string]any{
		"connection": name, "query": issue1678BadDocument, "purpose": issue1678Purpose,
	})
	if status, _ := out["status"].(float64); status != http.StatusOK || out["upstream_error"] != true {
		t.Fatalf("this criterion is about a failure inside a 200; the result was status=%v upstream_error=%v", out["status"], out["upstream_error"])
	}
	errs, _ := out["errors"].([]any)
	first, _ := errs[0].(map[string]any)
	upstreamMessage, _ := first["message"].(string)
	if upstreamMessage == "" {
		t.Fatalf("the upstream reported no message to name: %v", out)
	}

	event := issue1678AuditEvent(t, c, callID)
	if event["success"] != false {
		t.Errorf("audit success = %v; want false", event["success"])
	}
	if msg, _ := event["error_message"].(string); !strings.Contains(msg, upstreamMessage) {
		t.Errorf("audit error_message = %q; want it to name the upstream's first error %q", msg, upstreamMessage)
	}
}

// TestIssue1678_TheCallRecordAndItsFetchSayTheCallFailed reads the same call
// where an agent does: the catalog, and the record fetch hands back.
func TestIssue1678_TheCallRecordAndItsFetchSayTheCallFailed(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "1678-record", map[string]any{"schema_validation": "warn"})

	out, callID := issue1678Call(t, c, issue1277QueryTool, map[string]any{
		"connection": name, "query": issue1678BadDocument, "purpose": issue1678Purpose,
	})
	if out["upstream_error"] != true {
		t.Fatalf("the upstream accepted the document meant to fail: %v", out)
	}

	record := issue1678CallRecord(t, c, name, callID)
	if record["success"] != false || record["outcome"] != "failed" {
		t.Errorf("catalog record success=%v outcome=%v; want false/failed", record["success"], record["outcome"])
	}
	if msg, _ := record["error_message"].(string); msg == "" {
		t.Error("the catalog record carries no error message")
	}

	fetched := issue1678Fetch(t, c, callID)
	if fetched["success"] != false || fetched["outcome"] != "failed" {
		t.Errorf("fetch mcp:call: success=%v outcome=%v; want false/failed", fetched["success"], fetched["outcome"])
	}
}

// TestIssue1678_ASuccessfulCallIsStillASuccess is the control: the same
// connection, a document the resolver answers, audited and cataloged as
// before.
func TestIssue1678_ASuccessfulCallIsStillASuccess(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "1678-control", map[string]any{"schema_validation": "warn"})

	out, callID := issue1678Call(t, c, issue1277QueryTool, map[string]any{
		"connection": name, "query": `query Ok { __typename }`, "purpose": issue1678Purpose,
	})
	if out["upstream_error"] != false {
		t.Fatalf("the control document failed: %v", out)
	}
	event := issue1678AuditEvent(t, c, callID)
	if event["success"] != true {
		t.Errorf("audit success = %v; want true", event["success"])
	}
	if msg, _ := event["error_message"].(string); msg != "" {
		t.Errorf("a successful call carries an error message: %q", msg)
	}
	record := issue1678CallRecord(t, c, name, callID)
	if record["success"] != true || record["outcome"] != "ran" {
		t.Errorf("catalog record success=%v outcome=%v; want true/ran", record["success"], record["outcome"])
	}
}

// TestIssue1678_AnExportOfAFailedDocumentIsAuditedAsAFailure holds
// graphql_export to the same rule api_export is held to: the export lands,
// carrying the errors, and the call is a failure in the audit log.
func TestIssue1678_AnExportOfAFailedDocumentIsAuditedAsAFailure(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "1678-export", map[string]any{"schema_validation": "warn"})

	out, callID := issue1678Call(t, c, issue1675ExportTool, map[string]any{
		"connection": name, "query": issue1678BadDocument, "purpose": issue1678Purpose,
		"name": "acc-1678-failed-export.json",
	})
	if out["upstream_error"] != true {
		t.Fatalf("graphql_export did not report the failure it exported: %v", out)
	}
	if id, _ := out["asset_id"].(string); id == "" {
		t.Errorf("the export landed no asset; the errors are what a reader opens: %v", out)
	}
	event := issue1678AuditEvent(t, c, callID)
	if event["success"] != false {
		t.Errorf("audit success = %v; want false", event["success"])
	}
	if msg, _ := event["error_message"].(string); msg == "" {
		t.Error("the audit row of the failed export carries no error message")
	}
}
