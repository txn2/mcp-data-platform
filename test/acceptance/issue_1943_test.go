//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Issue #1943: the script page shows each version's agreed change summary,
// and administrators see the scripts saved before the harness that still
// carry lint findings or have no tests. These criteria save scripts through
// manage_script on the running platform and read the routes the portal's
// pages read: the version history the script page draws its "What changed"
// section from, and the administrators' view behind the "Saved before tests"
// tab. The pages themselves are checked in the browser against the same
// stack, in light and dark mode (build/1943/acceptance.md).
//
// Wire forms: manage_script's command, name, source and change_summary are
// typed strings and user_agreed a boolean, so each admits one JSON form and
// is sent as a literal tools/call parameter of it. The REST routes take no
// body.

const summary1943 = "The report now hands back twice the count."

func TestIssue1943_AVersionsAgreedSummaryIsWhatTheScriptPageReads(t *testing.T) {
	c := connect(t)
	name := "acceptance-1943-summary-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	created := c.saveScript(map[string]any{"command": "create", "name": name, "source": `def main():
    """Hands back the count."""
    platform.result({"n": 1})
`}, nil)
	id, _ := created["id"].(string)

	edit := c.tested(map[string]any{"command": "update", "name": name, "source": `def main():
    """Hands back twice the count."""
    platform.result({"n": 2})
`}, nil)
	edit["change_summary"] = summary1943
	edit["user_agreed"] = true
	if out := c.call("manage_script", edit); out["status"] != "updated" {
		t.Fatalf("the edit with its agreed summary must save: %v", out)
	}

	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/versions", nil)
	versions, _ := body["data"].([]any)
	if status != http.StatusOK || len(versions) != 2 {
		t.Fatalf("the script page's history must list both versions: %d %v", status, body)
	}
	latest, _ := versions[0].(map[string]any)
	if latest["change_summary"] != summary1943 || latest["change_agreed_at"] == nil || latest["change_agreed_by"] == nil {
		t.Fatalf("the latest version must carry its summary, who agreed and when: %v", latest)
	}
	first, _ := versions[1].(map[string]any)
	if _, has := first["change_summary"]; has {
		t.Fatalf("the first version changed nothing and carries no summary: %v", first)
	}
}

// findings1943 is a script saved before the harness that carries two lint
// findings (a helper with no docstring, a variable never used) and a test.
const findings1943 = `def double(x):
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

// clean1943 is findings1943 with both findings cleared.
const clean1943 = `def double(x):
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

// legacyRow1943 is the administrators' view's row for the script id, if it
// lists one.
func legacyRow1943(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/scripts/legacy", nil)
	if status != http.StatusOK {
		t.Fatalf("the administrators' view answered %d: %v", status, body)
	}
	rows, _ := body["data"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["id"] == id {
			return row
		}
	}
	return nil
}

func TestIssue1943_TheViewListsALegacyScriptForAdministratorsAloneUntilItsFindingsAreCleared(t *testing.T) {
	c := connect(t)
	name := "acceptance-1943-legacy-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	created := c.seedPreGate(t, map[string]any{"command": "create", "name": name, "source": findings1943})
	id, _ := created["id"].(string)
	issue1904Exec(t, issue1904DB(t), `UPDATE scripts SET legacy = TRUE WHERE id = $1`, id)

	row := legacyRow1943(t, c, id)
	if row == nil {
		t.Fatalf("a script saved before the harness with lint findings must be listed")
	}
	if n, _ := row["lint_findings"].(float64); n < 2 || row["tests"] != float64(1) {
		t.Fatalf("the row must count its findings and its test: %v", row)
	}

	peer := connectAs(t, devOwnerAPIKey)
	if status, body := peer.rest(http.MethodGet, "/api/v1/admin/scripts/legacy", nil); status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("a non-administrator must not reach the view: %d %v", status, body)
	}

	cleared := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": clean1943})
	if cleared["status"] != "updated" {
		t.Fatalf("the cleared version must save: %v", cleared)
	}
	if row := legacyRow1943(t, c, id); row != nil {
		t.Fatalf("with no finding and a test the script must leave the view: %v", row)
	}
}
