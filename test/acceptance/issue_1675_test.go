//go:build integration

package acceptance

// Issue #1675: graphql_export was listed in the admin tool listing and
// registered on no MCP server, so the export a withheld graphql_query result
// points the caller at could not be called on any deployment.
//
// Every criterion runs against a graphql connection added at runtime through
// the admin API, against the same real GraphQL endpoint #1277's criteria use:
// DataHub's GMS at /api/graphql. No fixture stands in for the upstream.
//
// Wire forms: graphql_export's `variables` is typed ["object", "string"] and
// admits the object itself and a string holding that object's JSON. Both are
// sent as literal tools/call params and asserted to land the same export.
// `tags` is an array of strings, `resource` and `paginate` are objects, and
// `connection`, `query`, `operation_name`, `name`, `description` and
// `idempotency_key` are strings only; `create_public_link` is a boolean and
// `timeout_seconds` an integer.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	issue1675ExportTool = "graphql_export"
	issue1675Purpose    = "Acceptance for #1675: graphql_export is registered on the server that lists it."
)

// issue1675Document is a document every GraphQL endpoint answers: the query
// root's type name, behind a variable so both wire forms of `variables` carry
// something the endpoint acts on. __typename is a schema field, not the
// __schema / __type introspection the tools refuse.
const issue1675Document = `query Acc1675($show: Boolean!) { __typename @include(if: $show) }`

// issue1675Variables is what that document takes, in the object form.
func issue1675Variables() map[string]any { return map[string]any{"show": true} }

// issue1675VariablesJSON is the same values in the string form.
func issue1675VariablesJSON(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(issue1675Variables())
	if err != nil {
		t.Fatalf("marshal the variables: %v", err)
	}
	return string(raw)
}

// TestIssue1675_ExportIsListedOnTheServerWithItsTitleAndDescription is the
// first criterion, read where the defect showed: tools/list. The admin
// listing carried the name from the toolkit and the title from the server,
// so the row appeared with an empty title and the tool could not be called.
func TestIssue1675_ExportIsListedOnTheServerWithItsTitleAndDescription(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)

	var found *mcp.Tool
	for _, tool := range c.tools() {
		if tool.Name == issue1675ExportTool {
			found = tool
			break
		}
	}
	if found == nil {
		t.Fatalf("tools/list does not carry %s", issue1675ExportTool)
	}
	if found.Title == "" {
		t.Error("the listed tool carries no title")
	}
	if found.Description == "" {
		t.Error("the listed tool carries no description")
	}

	// platform_find_tools answers from the same inventory, and returned
	// nothing for this tool on any query.
	out := c.call("platform_find_tools", map[string]any{"query": "export a graphql result", "purpose": issue1675Purpose})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal the tool search: %v", err)
	}
	if !strings.Contains(string(raw), issue1675ExportTool) {
		t.Errorf("platform_find_tools does not return %s:\n%s", issue1675ExportTool, raw)
	}
}

// TestIssue1675_ExportRunsThroughTheAdminToolsCallRoute is the second
// criterion verbatim: POST /api/v1/admin/tools/call answered
// `unknown tool "graphql_export"` on the deployment that listed it.
func TestIssue1675_ExportRunsThroughTheAdminToolsCallRoute(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "admin-call", nil)

	status, body := c.rest(http.MethodPost, "/api/v1/admin/tools/call", jsonBody(t, map[string]any{
		"tool_name": issue1675ExportTool,
		"parameters": map[string]any{
			"connection": name,
			"query":      issue1675Document,
			"variables":  issue1675Variables(),
			"name":       fmt.Sprintf("acc-1675-admin-%s", name),
			"purpose":    issue1675Purpose,
		},
	}))
	if status != http.StatusOK {
		t.Fatalf("the admin tools/call route answered HTTP %d: %v", status, body)
	}
	if cause, _ := body["error"].(string); cause != "" {
		t.Fatalf("the admin tools/call route refused the export: %s", cause)
	}
	// The route answers 200 with is_error set when the tool itself refused,
	// which on the deployment the ticket was reported from was
	// `unknown tool "graphql_export"`.
	if failed, _ := body["is_error"].(bool); failed {
		t.Fatalf("the export was refused: %v", body["content"])
	}
}

// TestIssue1675_ExportLandsAnAssetInBothWireForms runs the tool through the
// MCP client the way a caller does, once per JSON form `variables` admits.
func TestIssue1675_ExportLandsAnAssetInBothWireForms(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "wire-forms", nil)

	forms := map[string]any{
		"object": issue1675Variables(),
		"string": issue1675VariablesJSON(t),
	}
	sizes := make(map[string]float64, len(forms))
	for form, variables := range forms {
		out := c.call(issue1675ExportTool, map[string]any{
			"connection": name,
			"query":      issue1675Document,
			"variables":  variables,
			"name":       fmt.Sprintf("acc-1675-%s-%s", form, name),
			"tags":       []any{"acceptance", "issue-1675"},
			"purpose":    issue1675Purpose,
		})
		if id, _ := out["asset_id"].(string); id == "" {
			t.Fatalf("%s form: the export created no asset: %v", form, out)
		}
		sizes[form] = number(t, out, "size_bytes")
		if sizes[form] <= 0 {
			t.Errorf("%s form: the export wrote %v bytes", form, sizes[form])
		}
	}
	if sizes["object"] != sizes["string"] {
		t.Errorf("the two wire forms wrote different results: %v vs %v", sizes["object"], sizes["string"])
	}
}

// TestIssue1675_AWithheldQueryResultCanBeFollowedToTheExport is the criterion
// the ticket was reported from: graphql_query withheld a result and told the
// caller to run graphql_export, which was a dead end.
func TestIssue1675_AWithheldQueryResultCanBeFollowedToTheExport(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	// A cap low enough that any answer at all is withheld.
	name := issue1277Connect(t, c, "withheld", map[string]any{"max_inline_bytes": 16})

	out := c.call(issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      issue1675Document,
		"variables":  issue1675Variables(),
		"purpose":    issue1675Purpose,
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal the withheld result: %v", err)
	}
	if !strings.Contains(string(raw), "export_arguments") {
		t.Fatalf("the result was not withheld with export arguments to follow:\n%s", raw)
	}

	args, _ := out["export_arguments"].(map[string]any)
	if args == nil {
		t.Fatalf("export_arguments is not an object: %v", out)
	}
	args["name"] = fmt.Sprintf("acc-1675-withheld-%s", name)
	args["purpose"] = issue1675Purpose
	export := c.call(issue1675ExportTool, args)
	if id, _ := export["asset_id"].(string); id == "" {
		t.Fatalf("following export_arguments created no asset: %v", export)
	}
}
