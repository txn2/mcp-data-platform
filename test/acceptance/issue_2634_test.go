//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance for #2634: a transform handler, a script whose
// transform(call, result) is what a covered caller gets back, on every path a
// result reaches them: a tool result, an export file and a script's read. A
// transform that protects a column refuses, for a covered caller, any SQL that
// uses the column other than by selecting it as it is. Calls go through the
// real client to the dev Trino (a memory-catalog table each run makes) and to
// the api-test fixture.
//
// Wire forms: manage_script handler_set takes kind "transform", connection a
// string, operations, personas, users and protected JSON arrays of strings,
// on_failure a string, approve a JSON boolean; trino_query takes connection
// and sql strings; the api_invoke_endpoint body is sent as a JSON object and as
// a string of JSON, which give the same transformed result.

const (
	// transformReader is the dev Trino connection the criteria read through.
	transformReader = "acme"
	// rawSSN is the protected value a run's table holds.
	rawSSN = "123-45-6789"
	// maskedSSN is what the masking transform turns it into.
	maskedSSN = "***-**-6789"
)

// maskTransform masks every ssn but its last four characters, in a row result
// and in an API body.
const maskTransform = `def transform(_call, result):
    """Masks every ssn but its last four characters."""
    for row in result.get("rows", []):
        if row.get("ssn"):
            row["ssn"] = "***-**-" + row["ssn"][-4:]
    body = result.get("body")
    if type(body) == "dict" and type(body.get("body")) == "dict":
        body = body["body"]
    if type(body) == "dict" and body.get("card"):
        body["card"]["number"] = "**** " + body["card"]["number"][-4:]
    return result
`

// tagTransform appends its letter to the result's order list, so two of them
// show the order they ran in.
func tagTransform(letter string) string {
	return fmt.Sprintf(`def transform(_call, result):
    """Records that it ran, after any transform before it."""
    body = result.get("body")
    if type(body) == "dict":
        body["order"] = body.get("order", "") + %q
    return result
`, letter)
}

// raisingTransform fails on every result.
const raisingTransform = `def transform(_call, _result):
    """Fails on every result."""
    fail("the transform could not run")
`

// augmentTransform adds the hash of a record it reads from another connection.
const augmentTransform = `def transform(_call, result):
    """Adds the owner record's hash, read from the TLS connection."""
    owner = platform.call("api_invoke_endpoint", {"connection": "api-test-fixture-tls", "method": "GET", "path": "/v1/fixed/owner"})
    result["body"]["owner_hash"] = owner["body"]["hash"]
    return result
`

// protectedTable makes a memory-catalog table with one row holding rawSSN and
// returns its full name.
func protectedTable(t *testing.T, admin *client) string {
	t.Helper()
	table := "memory.default." + strings.ReplaceAll(handlerNonce("acc_2634"), "-", "_")
	admin.call("trino_execute", map[string]any{
		"purpose":    handlerPurpose,
		"connection": scratchConnection,
		"sql":        "CREATE TABLE " + table + " AS SELECT * FROM (VALUES ('Ana', '" + rawSSN + "', 7)) AS t(name, ssn, dept)",
	})
	t.Cleanup(func() {
		admin.callRaw("trino_execute", map[string]any{"purpose": handlerPurpose, "connection": scratchConnection, "sql": "DROP TABLE IF EXISTS " + table})
	})
	return table
}

// trinoRead runs sql through trino_query on the reader as c.
func trinoRead(t *testing.T, c *client, sql string) (*mcp.CallToolResult, string) {
	t.Helper()
	res, text, err := c.callRaw("trino_query", map[string]any{"purpose": handlerPurpose, "connection": transformReader, "sql": sql})
	if err != nil {
		t.Fatalf("trino_query: transport error: %v", err)
	}
	return res, text
}

// TestIssue2634_AProtectedColumnIsMaskedForACoveredPersona: a transform
// protecting a column masks it in trino_query results for a covered persona
// and leaves it whole for a persona it does not cover.
func TestIssue2634_AProtectedColumnIsMaskedForACoveredPersona(t *testing.T) {
	admin := connect(t)
	persona, covered, _ := tempPersona(t, admin, "acc-2634-mask", nil)
	table := protectedTable(t, admin)
	name := handlerNonce("acc-2634-mask")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": transformReader, "personas": []string{persona}, "protected": []string{table + ".ssn"}})
	if res, text := trinoRead(t, covered, "SELECT name, ssn FROM "+table); res.IsError || !strings.Contains(text, maskedSSN) || strings.Contains(text, rawSSN) {
		t.Errorf("the covered persona's result is not masked: %s", text)
	}
	if res, text := trinoRead(t, admin, "SELECT name, ssn FROM "+table); res.IsError || !strings.Contains(text, rawSSN) {
		t.Errorf("a persona the transform does not cover got a changed result: %s", text)
	}
}

// TestIssue2634_TheColumnIsMaskedInAnExportAndAScriptRead: the same column is
// masked in a trino_export file and in a script's read for the covered caller.
func TestIssue2634_TheColumnIsMaskedInAnExportAndAScriptRead(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	table := protectedTable(t, admin)
	name := handlerNonce("acc-2634-paths")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": transformReader, "users": []string{devOwnerEmail}, "protected": []string{table + ".ssn"}})

	_, out := exportCall(t, owner, map[string]any{
		"connection": transformReader, "sql": "SELECT name, ssn FROM " + table, "format": "csv",
		"name": handlerNonce("acc-2634-export"), "description": "Acceptance #2634: a masked export.",
		"purpose": "Acceptance #2634: a transform masks an export.",
	})
	id := fmt.Sprint(out["asset_id"])
	status, content := owner.restText("/api/v1/portal/assets/" + id + "/content")
	if status != http.StatusOK || !strings.Contains(content, maskedSSN) || strings.Contains(content, rawSSN) {
		t.Errorf("the export file is not masked: %d %s", status, content)
	}

	reader := handlerNonce("acc-2634-reader")
	src := fmt.Sprintf(`def main():
    """Reads the protected table."""
    rows = platform.query(connection = %q, sql = "SELECT name, ssn FROM %s")
    platform.result(rows)
`, transformReader, table)
	owner.saveScript(map[string]any{"command": "create", "name": reader, "source": src}, nil)
	ran := runScript1551(t, owner, reader, map[string]any{})
	raw, _ := json.Marshal(ran)
	if !strings.Contains(string(raw), maskedSSN) || strings.Contains(string(raw), rawSSN) {
		t.Errorf("the script's read is not masked: %s", raw)
	}
}

// TestIssue2634_AProtectedColumnIsUsedOnlyAsItIs: substr, a WHERE, a JOIN and
// a GROUP BY on the protected column are refused for the covered persona,
// naming the column and the handler.
func TestIssue2634_AProtectedColumnIsUsedOnlyAsItIs(t *testing.T) {
	admin := connect(t)
	persona, covered, _ := tempPersona(t, admin, "acc-2634-uses", nil)
	table := protectedTable(t, admin)
	name := handlerNonce("acc-2634-uses")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": transformReader, "personas": []string{persona}, "protected": []string{table + ".ssn"}})
	for label, sql := range map[string]string{
		"substr":   "SELECT substr(ssn, 1, 3) FROM " + table,
		"where":    "SELECT name FROM " + table + " WHERE ssn = '" + rawSSN + "'",
		"join":     "SELECT a.name FROM " + table + " a JOIN " + table + " b ON a.ssn = b.ssn",
		"group by": "SELECT ssn, count(*) FROM " + table + " GROUP BY ssn",
	} {
		res, text := trinoRead(t, covered, sql)
		if !res.IsError || !strings.Contains(text, table+".ssn") || !strings.Contains(text, name) {
			t.Errorf("%s on the protected column was not refused naming the column and %s: %s", label, name, text)
		}
		if strings.Contains(text, rawSSN[:3]+"\"") {
			t.Errorf("%s leaked part of the value: %s", label, text)
		}
	}
}

// TestIssue2634_AProtectedPathIsMaskedInAnAPIResultAndExport: a transform
// protecting a JSON path masks it in api_invoke_endpoint and api_export, the
// body sent in either form giving the same transformed result.
func TestIssue2634_AProtectedPathIsMaskedInAnAPIResultAndExport(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2634-api")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}, "protected": []string{"body.body.card.number"}})
	card := map[string]any{"card": map[string]any{"number": "4111 1111 1111 1111"}}
	results := map[string]string{}
	for form, body := range bodyForms(t, card) {
		res, text := echoPut(t, owner, handlerNonce("t2634-api"), body)
		if res.IsError || !strings.Contains(text, "**** 1111") || strings.Contains(text, "4111 1111 1111 1111") {
			t.Errorf("body as %s: the result is not masked: %s", form, text)
		}
		results[form] = maskedPart(text)
	}
	if results["object"] != results["string"] {
		t.Errorf("the two body forms were transformed differently:\n%s\n%s", results["object"], results["string"])
	}
	res, text, err := owner.callRaw("api_export", map[string]any{
		"connection": handlerConnection, "method": "PUT", "path": "/v1/echo", "body": card,
		"name": handlerNonce("acc-2634-apiexport"), "description": "Acceptance #2634: a masked API export.",
		"purpose": "Acceptance #2634: a transform masks an API export.",
	})
	if err != nil || res.IsError {
		t.Fatalf("api_export: %v %s", err, text)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	status, content := owner.restText("/api/v1/portal/assets/" + fmt.Sprint(out["asset_id"]) + "/content")
	if status != http.StatusOK || !strings.Contains(content, "**** 1111") || strings.Contains(content, "4111 1111 1111 1111") {
		t.Errorf("the API export file is not masked: %d %s", status, content)
	}
}

// TestIssue2634_AnAugmentingTransformReadsWhatThePersonaCannot: a transform
// adds a field it read from a connection the covered persona cannot reach;
// that persona still cannot call that connection.
func TestIssue2634_AnAugmentingTransformReadsWhatThePersonaCannot(t *testing.T) {
	admin := connect(t)
	persona, covered, body := tempPersona(t, admin, "acc-2634-aug", nil)
	body["deny_connections"] = []string{"api-test-fixture-tls"}
	if code := admin.restJSON(http.MethodPut, "/api/v1/admin/personas/"+persona, body); code != http.StatusOK {
		t.Fatalf("denying the TLS connection to %s: HTTP %d", persona, code)
	}
	name := handlerNonce("acc-2634-aug")
	saveHandlerScript(t, admin, name, augmentTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"operations": []string{"PUT /v1/echo"}, "personas": []string{persona}})
	res, text := echoPut(t, covered, handlerNonce("t2634-aug"), map[string]any{"id": 1})
	if res.IsError || !strings.Contains(text, "owner_hash") {
		t.Errorf("the transform did not add the field it read: %s", text)
	}
	direct, dtext, err := covered.callRaw("api_invoke_endpoint", map[string]any{"purpose": handlerPurpose, "connection": "api-test-fixture-tls", "method": "GET", "path": "/v1/fixed/owner"})
	if err != nil || !direct.IsError {
		t.Errorf("the covered persona reached the connection the transform read: %s", dtext)
	}
}

// TestIssue2634_TwoTransformsRunInTheOrderSet: two transforms on a connection
// run in the order an administrator set, and in the new order once changed.
func TestIssue2634_TwoTransformsRunInTheOrderSet(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	a, b := handlerNonce("acc-2634-a"), handlerNonce("acc-2634-b")
	saveHandlerScript(t, admin, a, tagTransform("A"), "transform")
	saveHandlerScript(t, admin, b, tagTransform("B"), "transform")
	ha := bindHandler(t, admin, a, "", "transform", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}, "order": 1})
	bindHandler(t, admin, b, "", "transform", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}, "order": 2})
	if _, text := echoPut(t, owner, handlerNonce("t2634-ab"), map[string]any{}); !strings.Contains(text, `"order":"AB"`) && !strings.Contains(text, `"order": "AB"`) {
		t.Errorf("the transforms did not run A then B: %s", text)
	}
	admin.handlerSet(a, "transform", map[string]any{"handler": idOf(ha), "order": 3})
	if _, text := echoPut(t, owner, handlerNonce("t2634-ba"), map[string]any{}); !strings.Contains(text, `"order":"BA"`) && !strings.Contains(text, `"order": "BA"`) {
		t.Errorf("after moving A last, the transforms did not run B then A: %s", text)
	}
}

// TestIssue2634_ATransformThatRaisesFailsTheCall: a transform that fails fails
// the call naming the handler; with return-original chosen and no protected
// fields, the caller gets the original result; return-original is refused for
// a transform that protects a field.
func TestIssue2634_ATransformThatRaisesFailsTheCall(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2634-raise")
	saveHandlerScript(t, admin, name, raisingTransform, "transform")
	h := bindHandler(t, admin, name, "", "transform", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	if res, text := echoPut(t, owner, handlerNonce("t2634-raise"), map[string]any{"v": "original"}); !res.IsError || !strings.Contains(text, name) {
		t.Errorf("a failing transform did not fail the call naming it: %s", text)
	}
	admin.handlerSet(name, "transform", map[string]any{"handler": idOf(h), "on_failure": "original"})
	if res, text := echoPut(t, owner, handlerNonce("t2634-orig"), map[string]any{"v": "original"}); res.IsError || !strings.Contains(text, "original") {
		t.Errorf("return-original did not give the original result: %s", text)
	}
	if out, text := admin.handlerSetRaw(name, "transform", map[string]any{"handler": idOf(h), "protected": []string{"body.v"}}); out != nil {
		t.Errorf("return-original was kept on a transform that protects a field: %v", out)
	} else if !strings.Contains(text, "protect") {
		t.Errorf("the refusal does not say why: %s", text)
	}
}

// TestIssue2634_AProtectedValueReachesNoCoveredSink: the protected value
// appears in no covered caller's tool result, export, script output, log,
// span or audit row, searched as written, base64, hex, URL- and JSON-escaped.
func TestIssue2634_AProtectedValueReachesNoCoveredSink(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	table := protectedTable(t, admin)
	name := handlerNonce("acc-2634-sink")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": transformReader, "users": []string{devOwnerEmail}, "protected": []string{table + ".ssn"}})
	sinks := map[string]string{}
	_, text := trinoRead(t, owner, "SELECT name, ssn FROM "+table)
	sinks["the tool result"] = text
	_, out := exportCall(t, owner, map[string]any{
		"connection": transformReader, "sql": "SELECT name, ssn FROM " + table, "format": "json",
		"name": handlerNonce("acc-2634-sinkexport"), "description": "Acceptance #2634: a masked export.",
		"purpose": "Acceptance #2634: no protected value in an export.",
	})
	_, sinks["the export"] = owner.restText("/api/v1/portal/assets/" + fmt.Sprint(out["asset_id"]) + "/content")
	reader := handlerNonce("acc-2634-sinkreader")
	owner.saveScript(map[string]any{"command": "create", "name": reader, "source": fmt.Sprintf(`def main():
    """Reads the protected table and prints it."""
    rows = platform.query(connection = %q, sql = "SELECT name, ssn FROM %s")
    print(rows)
    platform.result(rows)
`, transformReader, table)}, nil)
	ran := runScript1551(t, owner, reader, map[string]any{})
	raw, _ := json.Marshal(ran)
	sinks["the script's output"] = string(raw)
	rows := admin.list("/api/v1/admin/audit/events?session_id=" + owner.sessionID + "&per_page=50")
	raw, _ = json.Marshal(rows)
	sinks["audit rows"] = string(raw)
	sinks["logs and spans"] = devSinkText(t)
	for sink, text := range sinks {
		for _, form := range secretForms(rawSSN) {
			if strings.Contains(text, form) {
				t.Errorf("the protected value reached %s as %q", sink, form)
			}
		}
	}
}

// TestIssue2634_AnUnmatchedCallIsNoSlower: an unmatched tool call's p50 and
// p99 over 1,000 calls, with 50 transforms bound to other connections, are
// within 5% of the same calls with none bound.
func TestIssue2634_AnUnmatchedCallIsNoSlower(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	unmatchedLatency(t, admin, owner, "acc-2634-latency", "transform", maskTransform, "transform")
}

// maskedPart is the card object of an echoed result, for comparing two.
func maskedPart(text string) string {
	i := strings.Index(text, `"card"`)
	if i < 0 {
		return text
	}
	j := strings.Index(text[i:], "}")
	if j < 0 {
		return text[i:]
	}
	return text[i : i+j+1]
}

// people2634 builds a schema on the dev stack's application database holding
// one person and their ssn, and a view over them, and returns it.
func people2634(t *testing.T, admin *client) string {
	t.Helper()
	schema := "acc2634_" + unique1579()
	holdSchema(t, admin, conn2402, schema)
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2402, "schema": schema, "version": 1, "name": "people",
		"up": "CREATE TABLE people (name text NOT NULL, ssn text NOT NULL); INSERT INTO people VALUES ('Ana', '" + rawSSN + "')",
	})
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2402, "schema": schema, "version": 2, "name": "people_view",
		"up": "CREATE VIEW people_v AS SELECT name, ssn FROM people",
	})
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2402, "schema": schema})
	return schema
}

// TestIssue2634_AnApplicationDatabaseReadIsMaskedOnEveryPath: a script's read
// of an application database is masked for the covered persona however it
// reads (platform.query, a query in platform.transaction, platform.batch and
// every page of platform.query_iter), platform.copy_out, whose bytes no
// transform reads, is refused rather than writing the raw value, and the
// protected column used in an expression through a view is refused.
func TestIssue2634_AnApplicationDatabaseReadIsMaskedOnEveryPath(t *testing.T) {
	admin := connect(t)
	persona, covered, _ := tempPersona(t, admin, "acc-2634-appdb", nil)
	schema := people2634(t, admin)
	name := handlerNonce("acc-2634-appdb")
	saveHandlerScript(t, admin, name, maskTransform, "transform")
	bindHandler(t, admin, name, "", "transform", map[string]any{"connection": conn2402, "personas": []string{persona}, "protected": []string{schema + ".people.ssn"}})
	sel := "SELECT name, ssn FROM " + schema + ".people"
	ran := draft2428(covered, "acc-2634-appdb-read", fmt.Sprintf(`
def main():
    """Reads the protected column on every path."""
    print(platform.query(connection = %[2]q, sql = %[1]q)["rows"])

    def read(tx):
        """Reads in the transaction."""
        return tx.query(%[1]q)["rows"]

    print(platform.transaction(%[2]q, read))
    print(platform.batch(connection = %[2]q, statements = [%[1]q])[0]["rows"])
    for row in platform.query_iter(connection = %[2]q, sql = %[1]q, page_rows = 1):
        print(row)
`, sel, conn2402), true)
	log, _ := ran["log"].(string)
	if ran["status"] != "succeeded" {
		t.Fatalf("the reading draft did not finish: %v", ran)
	}
	if n := strings.Count(log, maskedSSN); n != 4 || strings.Contains(log, rawSSN) {
		t.Errorf("the reads are not masked on all four paths (%d masked): %s", n, log)
	}
	out := draft2428(covered, "acc-2634-appdb-copy", fmt.Sprintf(`
def main():
    """Copies the protected column out."""
    platform.copy_out(connection = %q, name = "people", sql = %q)
`, conn2402, sel), true)
	text := asJSON(out)
	if out["status"] == "succeeded" || !strings.Contains(text, "query_iter") || strings.Contains(text, rawSSN) {
		t.Errorf("copy_out under a transform was not refused naming the reads that pass through it: %s", text)
	}
	out = draft2428(covered, "acc-2634-appdb-view", fmt.Sprintf(`
def main():
    """Reads part of the protected column through a view."""
    print(platform.query(connection = %q, sql = "SELECT substr(ssn, 1, 3) AS part FROM %s.people_v"))
`, conn2402, schema), true)
	text = asJSON(out)
	if out["status"] == "succeeded" || !strings.Contains(text, "ssn") || !strings.Contains(text, name) || strings.Contains(text, rawSSN[:3]+`"`) {
		t.Errorf("an expression on the protected column through a view was not refused naming the column and %s: %s", name, text)
	}
}
