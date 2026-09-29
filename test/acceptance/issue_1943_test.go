//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Issue #1943: the script page shows each version's agreed change summary.
// The criterion saves a script through manage_script on the running platform
// and reads the route the script page draws its "What changed" section from.
// The page itself is checked in the browser against the same stack, in light
// and dark mode (build/1943/acceptance.md).
//
// Wire forms: manage_script's command, name, source and change_summary are
// typed strings and user_agreed a boolean, so each admits one JSON form and
// is sent as a literal tools/call parameter of it. The REST route takes no
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
