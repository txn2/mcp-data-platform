//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2683: route is the Site routing entry only in a script that
// declares INTEGRATION = "site_routing". An ordinary script whose top-level
// helper is named route (one parameter, or two) saves, is absent from the
// Site routing picker and cannot be chosen, the refusal naming the line to
// add; the same script with the declaration added is offered and chosen.
//
// Wire forms: manage_script takes command, name, description and source as
// strings, sent as literal tools/call params; the admin integration routes
// take a JSON object with script_id as a string, interval_seconds as a JSON
// number and approve as a JSON boolean.

// helperRoute2683 is a scheduled-style script whose route(x) is an internal
// dispatch helper, the shape v1.162.2 offered in the picker.
const helperRoute2683 = `def main():
    """Plans the day's approvals."""
    print(route("invoice"))

def route(kind):
    """Picks the queue an item goes to."""
    return "queue-" + kind
`

// twoParamRoute2683 is a script whose route helper takes two parameters, which
// v1.162.2 refused to save.
const twoParamRoute2683 = `def main():
    """Adds."""
    print(route(1, 2))

def route(a, b):
    """Adds two numbers."""
    return a + b
`

func TestIssue2683_ARouteHelperIsNotARoutingScript(t *testing.T) {
	clear2659(t)
	admin := connect(t)
	save := func(prefix, source string) (string, string) {
		t.Helper()
		name := prefix + unique1579()
		out := admin.saveScript(map[string]any{
			"command": "create", "name": name, "description": "A route helper (acceptance #2683)", "source": source,
		}, nil)
		id, _ := out["id"].(string)
		if id == "" {
			t.Fatalf("saving %s: %v", name, out)
		}
		t.Cleanup(func() { admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
		return id, name
	}
	helperID, _ := save("acc-2683-helper-", helperRoute2683)
	twoID, _ := save("acc-2683-two-", twoParamRoute2683)

	status, got := rest2559(t, keyAdmin2604, http.MethodGet, adminIntegrations2659+"/"+op2659+"/scripts?search=acc-2683-", nil)
	want2559(t, "listing the picker's scripts", status, http.StatusOK, got)
	raw, _ := got["_raw"].(string)
	for _, id := range []string{helperID, twoID} {
		if strings.Contains(raw, id) {
			t.Errorf("the picker offers %s, whose route is a helper: %s", id, raw)
		}
	}

	status, got = rest2559(t, keyAdmin2604, http.MethodPut, adminIntegrations2659+"/"+op2659,
		map[string]any{"script_id": helperID, "interval_seconds": 900, "approve": true})
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(got["_raw"]), `INTEGRATION = \"site_routing\"`) &&
		!strings.Contains(fmt.Sprint(got["_raw"]), `INTEGRATION = "site_routing"`) {
		t.Fatalf("choosing a script with a route helper answered %d, not a refusal naming the declaration: %v", status, got["_raw"])
	}
	if overview2659(t)["binding"] != nil {
		t.Error("the refused choice left a binding")
	}
}

func TestIssue2683_ADeclaredScriptIsOfferedAndChosen(t *testing.T) {
	clear2659(t)
	admin := connect(t)
	name := "acc-2683-declared-" + unique1579()
	out := admin.seedPreGate(t, map[string]any{
		"command": "create", "name": name, "description": "Declares Site routing (acceptance #2683)",
		"source": "INTEGRATION = \"site_routing\"\n\ndef route(req):\n    \"\"\"Answers every hostname ready.\"\"\"\n    return {h[\"hostname\"]: {\"status\": \"ready\"} for h in req[\"hosts\"]}\n",
	})
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("saving the declared script: %v", out)
	}
	t.Cleanup(func() { admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	status, got := rest2559(t, keyAdmin2604, http.MethodGet, adminIntegrations2659+"/"+op2659+"/scripts?search=acc-2683-declared-", nil)
	want2559(t, "listing the picker's scripts", status, http.StatusOK, got)
	if raw, _ := got["_raw"].(string); !strings.Contains(raw, id) {
		t.Errorf("the picker does not offer %s, which declares Site routing: %s", name, raw)
	}
	status, got = rest2559(t, keyAdmin2604, http.MethodPut, adminIntegrations2659+"/"+op2659,
		map[string]any{"script_id": id, "interval_seconds": 900, "approve": true})
	want2559(t, "choosing the declared script", status, http.StatusOK, got)
	t.Cleanup(func() { clear2659(t) })
}
