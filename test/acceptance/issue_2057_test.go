//go:build integration

package acceptance

// Issue #2057: an export cut at a row or page cap returned a clean success
// with no flag. A query of roughly 237,600 rows exported with no limit wrote
// 100,000 of them and said "Exported 100000 rows as csv."
//
// What this holds, against the running platform and the dev stack's Trino,
// the api-test fixture and DataHub's GraphQL endpoint: a cut the caller did not
// ask for writes nothing and says why; a cut the caller asked for, or accepted
// with on_truncation "warn", is written and flagged in the response, on the
// asset (tag and version metadata, as the portal reads them), on a managed
// resource's version, and on a script run's output; a script whose export is
// refused fails its run; count expectations guard an export; and the cap is
// stated in trino_export's description and in platform_info.
//
// Wire forms: trino_export's `on_truncation` is typed string (enum fail/warn)
// and `limit`, `expect_rows` and `expect_min_rows` integer; api_export's and
// graphql_export's `paginate` and `resource` are objects and their
// `on_truncation` a string. Each admits that one JSON form and is sent in it as
// a literal tools/call param.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	issue2057Purpose = "Acceptance for #2057: an export cut at a limit is never a clean success."
	// issue2057Cap is portal.export.max_rows on the dev stack: the default.
	issue2057Cap = 100000
	// issue2057PastTheCap is 101,000 rows: one past the cap and then some.
	issue2057PastTheCap = "SELECT x FROM UNNEST(sequence(1, 1000)) a(x) CROSS JOIN UNNEST(sequence(1, 101)) b(y)"
	// issue2057Five is five rows in a fixed order.
	issue2057Five = "SELECT x FROM UNNEST(sequence(1, 5)) a(x) ORDER BY x"
)

// issue2057Export calls trino_export on the dev warehouse with args beside the
// required ones, and returns the result and its text.
func issue2057Export(t *testing.T, c *client, sql, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	call := map[string]any{
		"connection": scratchResourceConnection, "sql": sql, "format": "csv",
		"name": name, "purpose": issue2057Purpose,
	}
	for k, v := range args {
		call[k] = v
	}
	res, text, err := c.callRaw("trino_export", call)
	if err != nil {
		t.Fatalf("trino_export: transport error: %v", err)
	}
	if res.IsError {
		return nil, text, true
	}
	return issue2057Decode(t, text), text, false
}

// issue2057Decode reads a successful result's JSON text.
func issue2057Decode(t *testing.T, text string) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("the result is not a JSON object: %v\n%s", err, text)
	}
	return out
}

// issue2057Asset reads an asset the way the portal's asset page does.
func issue2057Asset(t *testing.T, c *client, id string) (tags []string, versions []map[string]any) {
	t.Helper()
	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET asset %s: HTTP %d %v", id, status, asset)
	}
	for _, tag := range asset["tags"].([]any) {
		tags = append(tags, tag.(string))
	}
	status, page := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET versions %s: HTTP %d %v", id, status, page)
	}
	for _, v := range page["data"].([]any) {
		versions = append(versions, v.(map[string]any))
	}
	return tags, versions
}

func issue2057HasTag(tags []string) bool {
	for _, tag := range tags {
		if tag == "_sys-truncated" {
			return true
		}
	}
	return false
}

func issue2057Delete(t *testing.T, c *client, id string) {
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
}

func TestIssue2057_AnExportPastTheDeploymentCapWritesNothing(t *testing.T) {
	c := connect(t)
	// A tag no other asset carries, so the listing below finds exactly what
	// this export wrote.
	tag := fmt.Sprintf("acc-2057-%d", time.Now().UnixNano())
	_, text, refused := issue2057Export(t, c, issue2057PastTheCap, tag, map[string]any{"tags": []any{tag}})
	if !refused {
		t.Fatalf("an export the deployment cap cut succeeded: %s", text)
	}
	for _, want := range []string{
		"the deployment cap of 100000 rows (portal.export.max_rows)",
		"the query returned more rows, so nothing was written",
		"Set limit to export a chosen subset",
		"set on_truncation to",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %q lacks %q", text, want)
		}
	}
	// Nothing was written: no asset carries the tag.
	status, list := c.rest(http.MethodGet, "/api/v1/portal/assets?tag="+tag, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("list assets: HTTP %d", status)
	}
	if data, _ := list["data"].([]any); len(data) != 0 {
		t.Errorf("a refused export left %d assets tagged %s", len(data), tag)
	}
}

func TestIssue2057_UnderWarnTheCutFileIsWrittenAndMarked(t *testing.T) {
	c := connect(t)
	out, text, refused := issue2057Export(t, c, issue2057PastTheCap,
		fmt.Sprintf("acc-2057-warn-%d", time.Now().UnixNano()), map[string]any{"on_truncation": "warn"})
	if refused {
		t.Fatalf("on_truncation warn was refused: %s", text)
	}
	id, _ := out["asset_id"].(string)
	issue2057Delete(t, c, id)
	if out["truncated"] != true || out["limit_source"] != "deployment" || out["limit_unit"] != "rows" ||
		number(t, out, "limit_applied") != issue2057Cap || number(t, out, "row_count") != issue2057Cap {
		t.Fatalf("the response does not flag the cut: %v", out)
	}
	if out["arbitrary_subset"] != true {
		t.Errorf("a cut of a query with no ORDER BY must say the subset is arbitrary: %v", out)
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "Truncated at the deployment cap of 100000 rows (portal.export.max_rows); the query returned more rows. This file is incomplete.") {
		t.Errorf("message = %q", msg)
	}

	tags, versions := issue2057Asset(t, c, id)
	if !issue2057HasTag(tags) {
		t.Errorf("the asset is not tagged _sys-truncated: %v", tags)
	}
	meta, _ := versions[0]["metadata"].(map[string]any)
	if meta["truncated"] != true || meta["limit_applied"] != float64(issue2057Cap) || meta["limit_source"] != "deployment" {
		t.Errorf("the version does not record the cut: %v", versions[0])
	}

	// A complete version, here one a person uploads in the portal, takes the
	// mark off; a revert to the cut version puts it back.
	status, body := c.rest(http.MethodPut, "/api/v1/portal/assets/"+id+"/content", strings.NewReader("x\n1\n"))
	if status != http.StatusOK {
		t.Fatalf("PUT content: HTTP %d %v", status, body)
	}
	if tags, _ = issue2057Asset(t, c, id); issue2057HasTag(tags) {
		t.Errorf("a complete version left the tag on: %v", tags)
	}
	status, body = c.rest(http.MethodPost, "/api/v1/portal/assets/"+id+"/versions/1/revert", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("revert: HTTP %d %v", status, body)
	}
	if tags, _ = issue2057Asset(t, c, id); !issue2057HasTag(tags) {
		t.Errorf("a revert to the cut version did not bring the tag back: %v", tags)
	}
}

func TestIssue2057_TheCallersOwnLimitIsWrittenAndFlagged(t *testing.T) {
	c := connect(t)
	out, text, refused := issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-limit-%d", time.Now().UnixNano()), map[string]any{"limit": 3})
	if refused {
		t.Fatalf("an export cut by the caller's limit was refused: %s", text)
	}
	issue2057Delete(t, c, out["asset_id"].(string))
	if out["truncated"] != true || out["limit_source"] != "request" || number(t, out, "limit_applied") != 3 ||
		number(t, out, "row_count") != 3 {
		t.Errorf("the response does not flag the requested cut: %v", out)
	}
	if _, said := out["arbitrary_subset"]; said {
		t.Errorf("an ORDER BY fixes which rows were kept: %v", out)
	}

	// Exactly at the limit is complete: the off-by-one case.
	out, text, refused = issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-exact-%d", time.Now().UnixNano()), map[string]any{"limit": 5})
	if refused {
		t.Fatalf("an export exactly at its limit was refused: %s", text)
	}
	issue2057Delete(t, c, out["asset_id"].(string))
	if out["truncated"] != false || number(t, out, "row_count") != 5 {
		t.Errorf("an export exactly at its limit is not cut: %v", out)
	}

	// The caller may refuse even its own cut.
	_, text, refused = issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-limitfail-%d", time.Now().UnixNano()), map[string]any{"limit": 3, "on_truncation": "fail"})
	if !refused || !strings.Contains(text, "the requested limit of 3 rows (limit)") {
		t.Errorf("on_truncation fail on a requested cut: refused=%v %s", refused, text)
	}
}

func TestIssue2057_ALimitAboveTheCapIsRefused(t *testing.T) {
	c := connect(t)
	_, text, refused := issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-over-%d", time.Now().UnixNano()), map[string]any{"limit": issue2057Cap + 1})
	if !refused || !strings.Contains(text, "limit 100001 exceeds deployment maximum of 100000 rows") {
		t.Errorf("a limit above the cap: refused=%v %s", refused, text)
	}
}

func TestIssue2057_ACountExpectationGuardsTheExport(t *testing.T) {
	c := connect(t)
	_, text, refused := issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-expect-%d", time.Now().UnixNano()), map[string]any{"expect_min_rows": 6})
	if !refused || !strings.Contains(text, "expected at least 6 rows, got 5; nothing was written.") {
		t.Errorf("a missed minimum: refused=%v %s", refused, text)
	}
	out, text, refused := issue2057Export(t, c, issue2057Five,
		fmt.Sprintf("acc-2057-expectwarn-%d", time.Now().UnixNano()), map[string]any{"expect_rows": 4, "on_truncation": "warn"})
	if refused {
		t.Fatalf("a missed count under warn was refused: %s", text)
	}
	issue2057Delete(t, c, out["asset_id"].(string))
	if out["expect_mismatch"] != "expected exactly 4 rows, got 5" {
		t.Errorf("expect_mismatch = %v", out["expect_mismatch"])
	}
}

func TestIssue2057_AResourceDestinationRecordsTheCut(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("acc-2057-%d.csv", time.Now().UnixNano())
	out, text, refused := issue2057Export(t, c, issue2057Five, "acc-2057-resource", map[string]any{
		"limit": 2, "resource": map[string]any{"path": "acceptance-2057", "filename": filename},
	})
	if refused {
		t.Fatalf("the resource export was refused: %s", text)
	}
	landing, _ := out["resource"].(map[string]any)
	id, _ := landing["resource_id"].(string)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "resource_id": id, "force": true})
	})
	if out["truncated"] != true || id == "" {
		t.Fatalf("out = %v", out)
	}
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET resource versions: HTTP %d %v", status, body)
	}
	versions, _ := body["versions"].([]any)
	head, _ := versions[0].(map[string]any)
	meta, _ := head["metadata"].(map[string]any)
	if meta["truncated"] != true || meta["limit_applied"] != float64(2) {
		t.Errorf("the resource's version does not record the cut: %v", head)
	}
}

func TestIssue2057_AnAPIWalkStoppedByItsBoundIsACutFile(t *testing.T) {
	c := connect(t)
	walk := func(name string, extra map[string]any) (map[string]any, string, bool) {
		call := map[string]any{
			"connection": apiTestConnection, "method": "GET", "path": "/v1/pagination/cursor",
			"paginate": map[string]any{"items": "items", "cursor_param": "cursor", "max_pages": 3},
			"name":     name, "purpose": issue2057Purpose,
		}
		for k, v := range extra {
			call[k] = v
		}
		res, text, err := c.callRaw("api_export", call)
		if err != nil {
			t.Fatalf("api_export: %v", err)
		}
		if res.IsError {
			return nil, text, true
		}
		return issue2057Decode(t, text), text, false
	}

	out, text, refused := walk(fmt.Sprintf("acc-2057-walk-%d", time.Now().UnixNano()), nil)
	if refused {
		t.Fatalf("a walk cut by the caller's max_pages was refused: %s", text)
	}
	id, _ := out["asset_id"].(string)
	issue2057Delete(t, c, id)
	if out["truncated"] != true || out["limit_source"] != "request" || out["limit_unit"] != "pages" ||
		number(t, out, "limit_applied") != 3 || out["stopped_by"] != "max_pages" || number(t, out, "items_merged") != 30 {
		t.Errorf("the walk's response does not flag the cut: %v", out)
	}
	if tags, versions := issue2057Asset(t, c, id); !issue2057HasTag(tags) || versions[0]["metadata"] == nil {
		t.Errorf("the walk's asset is not marked: tags %v versions %v", tags, versions)
	}

	_, text, refused = walk(fmt.Sprintf("acc-2057-walkfail-%d", time.Now().UnixNano()), map[string]any{"on_truncation": "fail"})
	if !refused || !strings.Contains(text, "the requested limit of 3 pages (paginate.max_pages)") {
		t.Errorf("on_truncation fail on a walk: refused=%v %s", refused, text)
	}
}

func TestIssue2057_AGraphQLWalkStoppedByItsBoundIsACutFile(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	conn := issue1277Connect(t, c, "acc-2057", nil)
	const document = `query Scroll($scrollId: String) {
	  scrollAcrossEntities(input: {query: "*", count: 1, scrollId: $scrollId}) {
	    nextScrollId
	    searchResults { entity { urn } }
	  }
	}`
	res, text, err := c.callRaw("graphql_export", map[string]any{
		"connection": conn, "query": document, "name": fmt.Sprintf("acc-2057-gql-%d", time.Now().UnixNano()),
		"paginate": map[string]any{
			"items": "scrollAcrossEntities.searchResults", "cursor_variable": "scrollId",
			"next_cursor_path": "scrollAcrossEntities.nextScrollId", "max_pages": 2,
		},
		"on_truncation": "warn", "purpose": issue2057Purpose,
	})
	if err != nil || res.IsError {
		t.Fatalf("graphql_export: %v %s", err, text)
	}
	out := issue2057Decode(t, text)
	issue2057Delete(t, c, out["asset_id"].(string))
	if out["truncated"] != true || out["limit_unit"] != "pages" || number(t, out, "limit_applied") != 2 {
		t.Errorf("the graphql walk does not flag the cut: %v", out)
	}
	if tags, _ := issue2057Asset(t, c, out["asset_id"].(string)); !issue2057HasTag(tags) {
		t.Errorf("the graphql walk's asset is not tagged: %v", tags)
	}
}

func TestIssue2057_AScriptRunSeesTheCut(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-2057-" + stamp
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2057: an export a run makes past the cap.",
		"source": fmt.Sprintf(`BIG = %q
SMALL = %q

def main():
    """Exports a generated list through trino_export."""
    sql = BIG if run.params["size"] == "big" else SMALL
    args = {"connection": %q, "sql": sql, "name": run.params["output"], "format": "csv"}
    if run.params["policy"]:
        args["on_truncation"] = run.params["policy"]
    platform.call("trino_export", args)
`, issue2057PastTheCap, issue2057Five, scratchResourceConnection),
		"params": []any{
			map[string]any{"name": "size", "type": "string", "required": true},
			map[string]any{"name": "policy", "type": "string"},
			map[string]any{"name": "output", "type": "string", "required": true},
		},
	}, map[string]any{"size": "small", "policy": "", "output": name + "-draft"})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	refused := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"size": "big", "policy": "", "output": name + "-refused"}, "wait_seconds": 300,
	})
	if refused["status"] != "failed" {
		t.Fatalf("a run whose export the cap cut did not fail: %v", refused)
	}
	if errText, _ := refused["error"].(string); !strings.Contains(errText, "portal.export.max_rows") {
		t.Errorf("the run's error does not name the cap: %v", refused["error"])
	}

	warned := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"size": "big", "policy": "warn", "output": name + "-warned"}, "wait_seconds": 300,
	})
	if warned["status"] != "succeeded" {
		t.Fatalf("the warn run did not succeed: %v", warned)
	}
	output := trinoExportOutput1854(warned["outputs"])
	if output == nil {
		t.Fatalf("the warn run lists no trino_export output: %v", warned["outputs"])
	}
	issue2057Delete(t, c, output["asset_id"].(string))
	if output["truncated"] != true || output["limit_applied"] != float64(issue2057Cap) || output["limit_unit"] != "rows" {
		t.Errorf("the run's output does not record the cut: %v", output)
	}
}

func TestIssue2057_TheCapIsStatedWhereAnAgentReadsIt(t *testing.T) {
	c := connect(t)
	var described string
	for _, tool := range c.tools() {
		if tool.Name == "trino_export" {
			raw, _ := json.Marshal(tool.InputSchema)
			described = string(raw)
		}
	}
	if !strings.Contains(described, "Maximum 100,000 rows on this deployment (portal.export.max_rows)") {
		t.Errorf("trino_export's schema does not state the cap: %s", described)
	}
	info := c.call("platform_info", map[string]any{})
	limits, _ := info["limits"].(map[string]any)
	export, _ := limits["export"].(map[string]any)
	if export["max_rows"] != float64(issue2057Cap) || export["max_rows_key"] != "portal.export.max_rows" {
		t.Errorf("platform_info limits.export = %v", limits["export"])
	}
	query, _ := limits["query"].(map[string]any)
	if query == nil || query["max_rows"] == nil {
		t.Errorf("platform_info limits.query = %v", limits["query"])
	}
	walk, _ := limits["page_walk"].(map[string]any)
	if api, _ := walk["api"].(map[string]any); api["default_max_pages"] != float64(100) {
		t.Errorf("platform_info limits.page_walk = %v", limits["page_walk"])
	}
}
