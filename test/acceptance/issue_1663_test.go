//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1663: an export lands a connection's response in a MANAGED RESOURCE at
// a path, create-or-replace, so a recurring pull is one rolling file with a
// version history rather than a new asset every run.
//
// Every criterion here runs through the real MCP surface against the running
// stack: the api-test fixture for `api_export`, the dev stack's Trino for
// `trino_export` and the table registered over what it lands, and a real
// managed script for `platform.export(destination="resources")`.
//
// Wire forms: the `resource` destination is a typed object with
// "additionalProperties": false, so the object is the ONLY form the schema
// admits; the string form of the same object is asserted to be REFUSED at the
// boundary rather than parsed. Its members (`path`, `filename`, `scope`,
// `scope_id`, `change_summary`) are strings, one form each, as are
// platform.export's `destination` and `key`. `api_export`'s own `query_params`
// values are untyped and both admitted forms are already covered by #1587.

const (
	issue1663Purpose    = "Acceptance for #1663: land a connection response in a managed resource by path."
	issue1663FolderPath = "acceptance/issue-1663"
)

// issue1663Destination is the resource destination one export names.
func issue1663Destination(filename string) map[string]any {
	return map[string]any{"path": issue1663FolderPath, "filename": filename}
}

// issue1663Export calls api_export at the fixture's whoami endpoint, which
// answers a small deterministic JSON document.
func issue1663Export(c *client, filename string, extra map[string]any) map[string]any {
	c.t.Helper()
	args := map[string]any{
		"connection": issue1587FixtureConn,
		"method":     "GET",
		"path":       "/v1/whoami",
		"name":       "Acceptance 1663 " + filename,
		"resource":   issue1663Destination(filename),
		"purpose":    issue1663Purpose,
	}
	for k, v := range extra {
		args[k] = v
	}
	return c.call("api_export", args)
}

// issue1663Landing reads the resource half of an export result, failing when
// the call landed an asset instead.
func issue1663Landing(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	if id, _ := out["asset_id"].(string); id != "" {
		t.Fatalf("the export wrote a portal asset %q instead of a managed resource: %v", id, out)
	}
	landing, _ := out["resource"].(map[string]any)
	if landing == nil {
		t.Fatalf("the export result carries no resource: %v", out)
	}
	return landing
}

// TestIssue1663_APIExportLandsAFileAtAPathAndVersionsIt is the ticket's first
// criterion: the first call creates the file, and the same call again is the
// NEXT VERSION of that same file rather than a second one.
func TestIssue1663_APIExportLandsAFileAtAPathAndVersionsIt(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("acc-1663-%d.json", time.Now().UnixNano())

	first := issue1663Landing(t, issue1663Export(c, filename, nil))
	reference, _ := first["reference"].(string)
	uri, _ := first["uri"].(string)
	id, _ := first["resource_id"].(string)
	if id == "" || !strings.HasPrefix(reference, "mcp:resource:") || !strings.HasPrefix(uri, "mcp://") {
		t.Fatalf("the landing does not name the file: %v", first)
	}
	if created, _ := first["created"].(bool); !created {
		t.Errorf("created = %v; want the first export at a fresh path to create the file", first["created"])
	}
	if v := number(t, first, "version"); v != 1 {
		t.Errorf("version = %v; want 1", v)
	}
	if !strings.HasSuffix(uri, issue1663FolderPath+"/"+filename) {
		t.Errorf("uri = %q; want the address the call named", uri)
	}
	if size := number(t, first, "size_bytes"); size <= 0 {
		t.Errorf("size_bytes = %v; want the bytes the upstream answered with", size)
	}

	// The reference the result carries dereferences to the file the export
	// wrote, which is what makes the result usable by the next call.
	doc := c.call("fetch", map[string]any{"reference": reference, "purpose": issue1663Purpose})
	if found, _ := doc["found"].(bool); !found {
		t.Fatalf("fetch did not resolve %s: %v", reference, doc)
	}

	second := issue1663Landing(t, issue1663Export(c, filename, nil))
	if got, _ := second["resource_id"].(string); got != id {
		t.Fatalf("the second export made a different file: %q then %q", id, got)
	}
	if got, _ := second["uri"].(string); got != uri {
		t.Errorf("uri = %q; want the address unchanged across the replacement", got)
	}
	if got, _ := second["reference"].(string); got != reference {
		t.Errorf("reference = %q; want the reference unchanged across the replacement", got)
	}
	if created, _ := second["created"].(bool); created {
		t.Errorf("created = %v; want the second export to replace rather than create", created)
	}
	if v := number(t, second, "version"); v != 2 {
		t.Errorf("version = %v; want the second export recorded as version 2", v)
	}
}

// TestIssue1663_AnUnsuccessfulResponseIsNotLanded: the file at that path has
// readers, so an error page must not become its next version.
func TestIssue1663_AnUnsuccessfulResponseIsNotLanded(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("acc-1663-status-%d.json", time.Now().UnixNano())
	first := issue1663Landing(t, issue1663Export(c, filename, nil))

	res, text, err := c.callRaw("api_export", map[string]any{
		"connection": issue1587FixtureConn,
		"method":     "GET",
		"path":       "/v1/there-is-no-such-endpoint",
		"name":       "Acceptance 1663 failed pull",
		"resource":   issue1663Destination(filename),
		"purpose":    issue1663Purpose,
	})
	if err != nil {
		t.Fatalf("api_export: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("an unsuccessful upstream response was landed in the library: %s", text)
	}
	if !strings.Contains(text, "404") || !strings.Contains(text, filename) {
		t.Errorf("the refusal does not name the status and the file: %s", text)
	}

	// The file still serves what it served before: the next successful export
	// is version 2, which it could not be had the failure landed.
	next := issue1663Landing(t, issue1663Export(c, filename, nil))
	if v := number(t, next, "version"); v != 2 {
		t.Errorf("version = %v; want 2, which means the refused call wrote nothing", v)
	}
	if got, _ := next["resource_id"].(string); got != first["resource_id"] {
		t.Errorf("the file's id moved: %v then %v", first["resource_id"], got)
	}
}

// TestIssue1663_AssetOnlyArgumentsAreRefused: an idempotency key answers a
// repeat call with the asset the first one made, which is the opposite of
// re-versioning one file, and a public share link is a portal asset's.
func TestIssue1663_AssetOnlyArgumentsAreRefused(t *testing.T) {
	c := connect(t)
	cases := map[string]struct {
		extra map[string]any
		want  string
	}{
		"an idempotency key": {map[string]any{"idempotency_key": "acc-1663"}, "idempotency_key"},
		"a public link":      {map[string]any{"create_public_link": true}, "create_public_link"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{
				"connection": issue1587FixtureConn, "method": "GET", "path": "/v1/whoami",
				"name":     "Acceptance 1663 refusal",
				"resource": issue1663Destination(fmt.Sprintf("acc-1663-refused-%d.json", time.Now().UnixNano())),
				"purpose":  issue1663Purpose,
			}
			for k, v := range tc.extra {
				args[k] = v
			}
			res, text, err := c.callRaw("api_export", args)
			if err != nil {
				t.Fatalf("api_export: transport error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("the call was not refused: %s", text)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("the refusal does not name %q: %s", tc.want, text)
			}
		})
	}
}

// TestIssue1663_TheDestinationIsAnObjectOnTheWire is the wire-form criterion.
// The destination's schema is a typed object closed to unknown keys, so the
// object is the only form it admits: the same content as a JSON string is
// refused at the boundary rather than parsed into a path nobody named.
func TestIssue1663_TheDestinationIsAnObjectOnTheWire(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("api_export", map[string]any{
		"connection": issue1587FixtureConn, "method": "GET", "path": "/v1/whoami",
		"name":     "Acceptance 1663 wire form",
		"resource": `{"path":"acceptance/issue-1663","filename":"acc-1663-string.json"}`,
		"purpose":  issue1663Purpose,
	})
	if err != nil {
		t.Fatalf("api_export: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("the string form of the destination was accepted: %s", text)
	}
	if !strings.Contains(text, "resource") {
		t.Errorf("the refusal does not name the offending argument: %s", text)
	}
}

// TestIssue1663_ALandedCSVIsQueryableAndTheTableFollowsIt is the ticket's
// motivating case, end to end: a recurring export lands one CSV, a table is
// registered over it, and the next export moves the table onto the new content
// and says so. `trino_export` carries the same destination `api_export` does.
func TestIssue1663_ALandedCSVIsQueryableAndTheTableFollowsIt(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	filename := "acc-1663-" + stamp + ".csv"

	export := func(rows string) map[string]any {
		c.t.Helper()
		return issue1663Landing(t, c.call("trino_export", map[string]any{
			"sql":      "SELECT " + rows,
			"format":   "csv",
			"name":     "Acceptance 1663 rows " + stamp,
			"resource": issue1663Destination(filename),
			"purpose":  issue1663Purpose,
		}))
	}

	first := export("1 AS store_id, 10 AS units")
	reference, _ := first["reference"].(string)
	if v := number(t, first, "version"); v != 1 {
		t.Fatalf("version = %v; want the first export to create the file: %v", v, first)
	}

	registration := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": "acc_1663_" + stamp,
	})
	table, _ := registration["query_table"].(string)
	if table == "" {
		t.Fatalf("manage_table did not register the landed file: %v", registration)
	}
	t.Cleanup(func() {
		if id, _ := registration["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})

	second := export("1 AS store_id, 11 AS units UNION ALL SELECT 2, 22")
	if v := number(t, second, "version"); v != 2 {
		t.Fatalf("version = %v; want the second export recorded as version 2: %v", v, second)
	}
	tables, _ := second["table_changes"].([]any)
	var followed bool
	for _, line := range tables {
		if text, _ := line.(string); strings.Contains(text, table) {
			followed = true
		}
	}
	if !followed {
		t.Fatalf("the export did not report what it did to %s: tables = %v", table, tables)
	}

	// The table serves the new content, which is the whole point of a file that
	// keeps its identity: nothing was re-registered.
	rows := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SELECT count(*) AS n FROM " + table,
		"purpose":    issue1663Purpose,
	})
	if !strings.Contains(fmt.Sprint(rows), "2") {
		t.Errorf("the registered table does not serve the second export's rows: %v", rows)
	}
}

// TestIssue1663_GraphQLExportLandsTheSameWay: the destination is one capability
// across every export tool, so the kind that reaches a GraphQL endpoint lands a
// file at a path on the same terms.
//
// It runs against the same real endpoint #1277's criteria run against, DataHub's
// GMS, and fails rather than skips when none answers: a gate that skips is a
// gate that was not run.
func TestIssue1663_GraphQLExportLandsTheSameWay(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	connection := issue1277Connect(t, c, "export-1663", nil)
	filename := fmt.Sprintf("acc-1663-graphql-%d.json", time.Now().UnixNano())

	export := func() map[string]any {
		t.Helper()
		return issue1663Landing(t, c.call("graphql_export", map[string]any{
			"connection": connection,
			"query":      "{ __typename }",
			"name":       "Acceptance 1663 graphql",
			"resource":   issue1663Destination(filename),
			"purpose":    issue1663Purpose,
		}))
	}

	first := export()
	if v := number(t, first, "version"); v != 1 {
		t.Fatalf("version = %v; want the first export to create the file: %v", v, first)
	}
	second := export()
	if second["resource_id"] != first["resource_id"] {
		t.Fatalf("the second export made a different file: %v then %v", first["resource_id"], second["resource_id"])
	}
	if v := number(t, second, "version"); v != 2 {
		t.Errorf("version = %v; want the second export recorded as version 2", v)
	}
}

// issue1663ScriptSource writes one output to the built-in managed-resource
// destination and records what it wrote, so the run's own record can be read
// back through the tool surface.
const issue1663ScriptSource = `
out = platform.export(
    name="Acceptance 1663 script output",
    rows=[{"store_id": 1, "units": 10}],
    format="csv",
    destination="resources",
    key="acceptance/issue-1663/%s",
)
platform.save_state({
    "resource_id": out["resource_id"],
    "reference": out["reference"],
    "uri": out["uri"],
    "version": str(out["version"]),
})
`

// TestIssue1663_AScriptOutputLandsInTheLibraryAndVersions is the ticket's third
// criterion: the same destination is reachable from platform.export, so a
// script's output can be a managed resource rather than only an asset or a
// bucket.
func TestIssue1663_AScriptOutputLandsInTheLibraryAndVersions(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1663-" + stamp
	filename := "acc-1663-script-" + stamp + ".csv"

	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1663: an output written to the managed-resource library.",
		"source":      fmt.Sprintf(issue1663ScriptSource, filename),
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	first := issue1663RunScript(t, c, name)
	if first["version"] != "1" {
		t.Fatalf("the first run recorded version %v; want 1: %v", first["version"], first)
	}
	reference, _ := first["reference"].(string)
	if !strings.HasPrefix(reference, "mcp:resource:") {
		t.Fatalf("the run's record carries no reference: %v", first)
	}
	uri, _ := first["uri"].(string)
	if !strings.HasSuffix(uri, issue1663FolderPath+"/"+filename) {
		t.Errorf("uri = %q; want the address the script's key named", uri)
	}

	second := issue1663RunScript(t, c, name)
	if second["resource_id"] != first["resource_id"] {
		t.Fatalf("the second run wrote a different file: %v then %v", first["resource_id"], second["resource_id"])
	}
	if second["version"] != "2" {
		t.Errorf("the second run recorded version %v; want 2", second["version"])
	}
	if second["uri"] != uri {
		t.Errorf("the file's address moved between runs: %v", second["uri"])
	}

	// The file the runs wrote is the caller's own, reachable by the reference
	// the run recorded.
	doc := c.call("fetch", map[string]any{"reference": reference, "purpose": issue1663Purpose})
	if found, _ := doc["found"].(bool); !found {
		t.Fatalf("fetch did not resolve the file the script wrote: %v", doc)
	}
}

// issue1663RunScript runs the script and returns the state it saved, which is
// the run's own record of what it wrote.
func issue1663RunScript(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	if state == nil {
		t.Fatalf("the run recorded no state: %v", got)
	}
	return state
}
