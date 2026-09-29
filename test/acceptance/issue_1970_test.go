//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1970: a library was shown as a script that does nothing. These
// criteria save a library and a script through manage_script and read what
// the portal reads: the listing narrowed by kind, the library's Flow graph and
// its grid tile. What the pages say, and the tile's picture, are checked in
// the browser in light and dark, as the owner and as an administrator
// (build/1970/acceptance.md).
//
// Wire forms: GET /api/v1/portal/scripts takes kind as a query string, sent
// once for each of script, library and the retired automation; the graph and
// thumbnail routes take path strings and variant as a query string, each sent
// once in the one form the route admits. manage_script's command, name,
// description and source are typed strings, each sent once as a literal
// tools/call parameter.

// issue1970Library is a library with two public functions, one private
// helper, and the test a save requires.
const issue1970Library = `"""Reporting windows."""

def last_week(today, days = 7):
    """The seven days before today. A window ends the day before."""
    return _span(today, days)

def quarter_of(month):
    """Which quarter a month falls in."""
    return (month - 1) // 3 + 1

def _span(today, days):
    """The days before today."""
    return [today - days, today]

def test_windows():
    """Both public functions answer."""
    assert.eq(last_week(10), [3, 10])
    assert.eq(quarter_of(5), 2)
`

// issue1970Listed reports whether the listing for kind names the script.
func issue1970Listed(t *testing.T, c *client, kind, name string) bool {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?scope=mine&kind="+kind, nil)
	if status != http.StatusOK {
		t.Fatalf("kind=%s: status %d: %v", kind, status, out)
	}
	rows, _ := out["data"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		sc, _ := row["script"].(map[string]any)
		if sc["name"] == name {
			return true
		}
	}
	return false
}

// TestIssue1970_TheKindFilterNamesScriptsAndLibraries: kind=script lists the
// scripts that run and not the library, kind=library the library and not the
// script, and kind=automation is refused as a kind that does not exist.
func TestIssue1970_TheKindFilterNamesScriptsAndLibraries(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	stamp := issue1941Stamp()
	lib := "acc-1970-lib-" + stamp
	issue1941CreateLibrary(t, c, lib, issue1970Library)
	_, runs := script1906(t, c, "kind-"+stamp, "def main():\n    \"\"\"Prints.\"\"\"\n    print(1)\n")

	if !issue1970Listed(t, c, "library", lib) || issue1970Listed(t, c, "library", runs) {
		t.Errorf("kind=library does not list exactly the library")
	}
	if !issue1970Listed(t, c, "script", runs) || issue1970Listed(t, c, "script", lib) {
		t.Errorf("kind=script does not list exactly the script")
	}
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?kind=automation", nil)
	if status != http.StatusBadRequest {
		t.Errorf("kind=automation answered %d, not 400: %v", status, out)
	}
	if detail := fmt.Sprint(out["detail"]); detail != `unknown kind "automation": must be script or library` {
		t.Errorf("the refusal says %q", detail)
	}
}

// TestIssue1970_ALibrarysFlowListsItsFunctionsAndTheLoadLine: the Flow tab's
// graph for a library names each public function with its parameters and its
// docstring's first sentence, and the load line at the version asked for;
// neither the private helper nor the library's test is offered.
func TestIssue1970_ALibrarysFlowListsItsFunctionsAndTheLoadLine(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	lib := "acc-1970-flow-" + issue1941Stamp()
	created := issue1941CreateLibrary(t, c, lib, issue1970Library)
	id := fmt.Sprint(created["id"])

	status, out := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/1/graph", id), nil)
	if status != http.StatusOK {
		t.Fatalf("graph: status %d: %v", status, out)
	}
	library, ok := out["library"].(map[string]any)
	if !ok {
		t.Fatalf("a library's graph carries no library block: %v", out)
	}
	fns, _ := library["functions"].([]any)
	want := []string{
		"last_week(today, days=7): The seven days before today.",
		"quarter_of(month): Which quarter a month falls in.",
	}
	got := make([]string, 0, len(fns))
	for _, f := range fns {
		fn, _ := f.(map[string]any)
		params, _ := fn["params"].([]any)
		sig := fmt.Sprint(fn["name"]) + "("
		for i, p := range params {
			if i > 0 {
				sig += ", "
			}
			sig += fmt.Sprint(p)
		}
		got = append(got, sig+"): "+fmt.Sprint(fn["doc"]))
	}
	// _span is private and test_windows is the library's test: neither is
	// a function a script loads.
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("functions = %q, want %q", got, want)
	}
	if load, want := fmt.Sprint(library["load"]), fmt.Sprintf(`load("lib:%s@1", "last_week", "quarter_of")`, lib); load != want {
		t.Errorf("load = %q, want %q", load, want)
	}

	// A script that runs carries no library block.
	sid, _ := script1906(t, c, "flow-"+issue1941Stamp(), "def main():\n    \"\"\"Prints.\"\"\"\n    print(1)\n")
	if _, out := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/1/graph", sid), nil); out["library"] != nil {
		t.Errorf("a script's graph carries a library block: %v", out["library"])
	}
}

// TestIssue1970_ALibraryIsGivenATile: the tile worker draws a library's tile,
// light and dark, rather than recording it as not drawable.
func TestIssue1970_ALibraryIsGivenATile(t *testing.T) {
	// The wait for the worker outlasts the default session deadline (#1738).
	c := connectAtFor(t, baseURL(), devOwnerAPIKey, tileWait+time.Minute)
	lib := "acc-1970-tile-" + issue1941Stamp()
	id := fmt.Sprint(issue1941CreateLibrary(t, c, lib, issue1970Library)["id"])

	light := awaitTile1909(t, c, id, "")
	if len(light) < 8 || light[1:4] != "PNG" {
		t.Fatalf("the tile is not a PNG: %q", light[:min(len(light), 8)])
	}
	if status, dark := tile1909(c, id, "dark"); status != http.StatusOK || dark == light {
		t.Errorf("the dark tile is %d and equal to the light one: %v", status, dark == light)
	}
}
