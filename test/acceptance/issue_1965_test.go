//go:build integration

package acceptance

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Issue #1965: the administrators' view of the scripts saved before the
// authoring gates is gone, and so is what it served: such a script is held to
// every rule on its next save, like any other. These criteria call the route
// the view read and the API reference the portal serves, and save an older
// script through manage_script on the running platform. The Automations admin
// page is checked in the browser against the same stack
// (build/1965/acceptance.md).
//
// Wire forms: manage_script's command, name and source are typed strings, so
// each admits one JSON form and is sent as a literal tools/call parameter of
// it. The REST routes take no body.

// legacyPath1965 is the route the removed view read.
const legacyPath1965 = "/api/v1/admin/scripts/legacy"

func TestIssue1965_TheViewsRouteIsGoneFromTheServerAndTheReference(t *testing.T) {
	c := connect(t)
	if status, body := c.rest(http.MethodGet, legacyPath1965, nil); status != http.StatusNotFound {
		t.Fatalf("GET %s answered %d, want 404: %v", legacyPath1965, status, body)
	}

	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+"/api/v1/admin/docs/doc.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("the API reference answered %d: %v", res.StatusCode, err)
	}
	if !strings.Contains(string(raw), `"/admin/scripts/runs"`) {
		t.Fatalf("the API reference does not list the admin script routes, so its absence proves nothing")
	}
	if strings.Contains(string(raw), `"/admin/scripts/legacy"`) {
		t.Errorf("the API reference still lists /admin/scripts/legacy")
	}
}

// older1965 is a script saved before the gates: two lint findings (a helper
// with no docstring, a variable never used) and a test.
const older1965 = `def double(x):
    return x * 2

def main():
    """Prints double two."""
    unused = 1
    print(double(2))

def test_main():
    """Prints four."""
    main()
    assert.eq(testing.outputs().log, "4\n")
`

// clean1965 is older1965 with both findings cleared.
const clean1965 = `def double(x):
    """Twice x."""
    return x * 2

def main():
    """Prints double two."""
    print(double(2))

def test_main():
    """Prints four."""
    main()
    assert.eq(testing.outputs().log, "4\n")
`

func TestIssue1965_AnOlderScriptRunsAndIsHeldToEveryRuleOnItsNextSave(t *testing.T) {
	c := connect(t)
	name := "acceptance-1965-older-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	c.seedPreGate(t, map[string]any{"command": "create", "name": name, "source": older1965})

	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if ran["status"] != "succeeded" {
		t.Fatalf("the older script must run as it is: %v", ran)
	}

	kept := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": "# a comment\n" + older1965})
	got := refusedRules1938(kept)
	if kept["status"] != "invalid" {
		t.Fatalf("a save carrying the older findings must be refused: %v", kept)
	}
	for _, rule := range []string{"missing-docstring", "unused-variable"} {
		if _, ok := got[rule]; !ok {
			t.Errorf("the refusal must name %s: got %v: %v", rule, got, kept)
		}
	}

	cleared := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": clean1965})
	if cleared["status"] != "updated" {
		t.Fatalf("the cleared version must save: %v", cleared)
	}
}
