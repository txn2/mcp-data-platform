//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Issue #2296: a Vega-Lite spec is an asset the portal draws. What is held
// here, through the surface an agent and a reader meet:
//
//   - a spec saved as application/vnd.vegalite+json opens as the chart on the
//     asset page, an authenticated share and a public share signed out, with
//     the spec one toggle away;
//   - the spec sent as a JSON object and as a string of JSON stores the same
//     spec and draws the same chart;
//   - a selection in one view filters a second view on the asset page;
//   - a spec loading a CSV managed resource draws its rows, and draws the new
//     rows after the resource is replaced, with no new version of the spec;
//   - a spec naming an external data URL is refused, naming the URL, and no
//     asset is created;
//   - a managed script exports a spec with platform.export and it draws;
//   - the tile is the chart, light and dark.
//
// Wire forms: save_asset's `content` admits a string or a JSON object or
// array; the spec is sent as an object and as a string of JSON, and an
// object under a type that is not JSON is refused. `references` is an array
// of strings, its one form. manage_resource's `action`, `reference` and
// `content`, manage_asset's `action` and `asset_id`, and run_script's `name`
// are typed string and sent as strings; run_script's `args` is an object and
// `wait_seconds` an integer, each sent in that form.

const issue2296Purpose = "Acceptance for #2296: a Vega-Lite spec drawn by the portal."

// issue2296Spec is two views over one data set: bars counting each category,
// with a point selection on them, and a point per row filtered by that
// selection. Five rows, three categories; picking "a" leaves two points.
var issue2296Spec = map[string]any{
	"$schema": "https://vega.github.io/schema/vega-lite/v6.json",
	"data": map[string]any{"values": []any{
		map[string]any{"cat": "a", "v": 1},
		map[string]any{"cat": "a", "v": 2},
		map[string]any{"cat": "b", "v": 3},
		map[string]any{"cat": "c", "v": 4},
		map[string]any{"cat": "c", "v": 5},
	}},
	"vconcat": []any{
		map[string]any{
			"params": []any{map[string]any{"name": "pick", "select": map[string]any{"type": "point", "fields": []any{"cat"}}}},
			"mark":   "bar",
			"encoding": map[string]any{
				"x": map[string]any{"field": "cat", "type": "nominal"},
				"y": map[string]any{"aggregate": "count", "type": "quantitative"},
			},
		},
		map[string]any{
			"transform": []any{map[string]any{"filter": map[string]any{"param": "pick"}}},
			"mark":      "point",
			"encoding": map[string]any{
				"x": map[string]any{"field": "v", "type": "quantitative"},
				"y": map[string]any{"field": "cat", "type": "nominal"},
			},
		},
	},
}

// issue2296CSVSpec draws one bar per row of the CSV resource it names.
const issue2296CSVSpec = `{
  "data": {"url": %q, "format": {"type": "csv"}},
  "mark": "bar",
  "encoding": {
    "x": {"field": "region", "type": "nominal"},
    "y": {"field": "units", "type": "quantitative"}
  }
}`

// issue2296ScriptSource exports a one-bar spec as a vegalite document. The
// spec is a module constant, the one place a literal may sit outside main().
const issue2296ScriptSource = `SPEC = '{"data": {"values": [{"k": "x", "n": 3}, {"k": "y", "n": 5}, {"k": "z", "n": 2}]}, "mark": "bar", "encoding": {"x": {"field": "k", "type": "nominal"}, "y": {"field": "n", "type": "quantitative"}}}'

def main():
    """Publishes a Vega-Lite chart of three bars."""
    out = platform.export(name = "Acceptance 2296 chart", rows = SPEC, format = "vegalite")
    print("exported", out.get("content_type"))
`

// sameJSON reports whether two JSON texts hold the same value.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, a)
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return reflect.DeepEqual(va, vb)
}

// TestIssue2296_ASpecSentAsAnObjectOrAStringDrawsWhereItIsRead: both wire
// forms store the same spec; it draws on three surfaces with the spec one
// toggle away, and its selection filters the second view.
func TestIssue2296_ASpecSentAsAnObjectOrAStringDrawsWhereItIsRead(t *testing.T) {
	c := connect(t)
	raw, err := json.Marshal(issue2296Spec)
	if err != nil {
		t.Fatal(err)
	}
	asObject := saveDrawn(t, c, "Acceptance 2296 object", "application/vnd.vegalite+json", issue2296Spec, issue2296Purpose)
	asString := saveDrawn(t, c, "Acceptance 2296 string", "application/vnd.vegalite+json", string(raw), issue2296Purpose)
	if !sameJSON(t, storedContent(t, c, asObject), string(raw)) || !sameJSON(t, storedContent(t, c, asString), string(raw)) {
		t.Fatal("the object and the string forms did not store the same spec")
	}

	points := &drawnMarks{Selector: "g.mark-symbol path", Count: 5}
	selection := &drawnSelection{Pick: "g.mark-rect path", Filtered: "g.mark-symbol path"}
	runDrawnSuite(t, []drawnCase{
		{
			Name: "Vega-Lite sent as an object", Kind: "vegalite", Asset: asObject,
			AuthShare: issue2373Share(t, c, asObject, "authenticated"), PublicShare: issue2373Share(t, c, asObject, "public"),
			SourceLabel: "View Spec", SourceText: `"vconcat"`, Marks: points, Selection: selection,
		},
		{
			Name: "Vega-Lite sent as a string", Kind: "vegalite", Asset: asString,
			PublicShare: issue2373Share(t, c, asString, "public"),
			SourceLabel: "View Spec", SourceText: `"vconcat"`, Marks: points,
		},
	})
}

// TestIssue2296_ASpecOnACSVResourceRedrawsWhenTheResourceIsReplaced: the
// spec names the file by reference, so replacing the file changes the chart
// and writes no version of the spec.
func TestIssue2296_ASpecOnACSVResourceRedrawsWhenTheResourceIsReplaced(t *testing.T) {
	c := connect(t)
	file := createResource1584(t, c, "persona", personaLibrary1584, fmt.Sprintf("chart-2296-%d.csv", time.Now().UnixNano()))
	saved := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("Acceptance 2296 csv %d", time.Now().UnixNano()),
		"content":      fmt.Sprintf(issue2296CSVSpec, file.uri),
		"content_type": "application/vnd.vegalite+json",
		"references":   []any{file.uri},
		"purpose":      issue2296Purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2296Purpose})
	})
	bars := func(n int) []drawnCase {
		return []drawnCase{{
			Name: fmt.Sprintf("Vega-Lite on a CSV resource, %d rows", n), Kind: "vegalite", Asset: id,
			SourceLabel: "View Spec", SourceText: `"csv"`, Marks: &drawnMarks{Selector: "g.mark-rect path", Count: n},
		}}
	}
	runDrawnSuite(t, bars(2))

	c.call("manage_resource", map[string]any{
		"action": "replace_content", "reference": "mcp:resource:" + file.id,
		"content": "region,units\nnorthwest,412\nsouthwest,318\nsoutheast,207\n",
	})
	runDrawnSuite(t, bars(3))

	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the asset: %d %v", status, asset)
	}
	if v, _ := asset["current_version"].(float64); v != 1 {
		t.Errorf("the spec's version is %v after the resource was replaced; want 1", asset["current_version"])
	}
}

// TestIssue2296_ASpecNamingAnExternalURLIsRefused: the refusal names the URL
// and nothing is written; an object under a type that is not JSON is refused
// too.
func TestIssue2296_ASpecNamingAnExternalURLIsRefused(t *testing.T) {
	c := connect(t)
	const external = "https://example.com/acceptance-2296.csv"
	text := refusedSave(t, c, "application/vnd.vegalite+json",
		map[string]any{"data": map[string]any{"url": external}, "mark": "bar"}, issue2296Purpose)
	if !strings.Contains(text, external) || !strings.Contains(text, "Nothing was written") {
		t.Errorf("the refusal does not name the URL: %s", text)
	}
	text = refusedSave(t, c, "text/markdown", issue2296Spec, issue2296Purpose)
	if !strings.Contains(text, "send the content as a string") {
		t.Errorf("an object under text/markdown was not refused for its form: %s", text)
	}
}

// TestIssue2296_AScriptExportsASpecThatDraws: platform.export's vegalite
// format writes a spec asset the portal draws.
func TestIssue2296_AScriptExportsASpecThatDraws(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2296-chart-%d", time.Now().UnixNano())
	authorScript1664(t, c, name, issue2296ScriptSource)
	run := runScript1834(t, c, name)
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	outputs, _ := run["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want one", run["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	id, _ := output["asset_id"].(string)
	if id == "" {
		t.Fatalf("the output names no asset: %v", output)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	if ct, _ := output["content_type"].(string); ct != "" && ct != "application/vnd.vegalite+json" {
		t.Errorf("the output is stored as %s", ct)
	}
	runDrawnSuite(t, []drawnCase{{
		Name: "Vega-Lite exported by a script", Kind: "vegalite", Asset: id,
		SourceLabel: "View Spec", SourceText: `"mark"`, Marks: &drawnMarks{Selector: "g.mark-rect path", Count: 3},
	}})
}

// TestIssue2296_TheTileIsTheChartLightAndDark: the platform's renderer draws
// the chart into the asset's tile in both schemes.
func TestIssue2296_TheTileIsTheChartLightAndDark(t *testing.T) {
	c := connect(t)
	id := saveDrawn(t, c, "Acceptance 2296 tile", "application/vnd.vegalite+json", issue2296Spec, issue2296Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, id, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f of its pixels differ from the background)", variant, ink)
		}
	}
}
