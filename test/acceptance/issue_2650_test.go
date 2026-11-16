//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2650: a policy, transform or watch is refused on a script
// whose version it would run does not define the kind's entry, naming the
// version and what the kind calls, from manage_script and the portal's route
// alike; a new handler naming no persona and no person is refused unless it
// says everyone; everyone binds it to every caller and is reported.
//
// Wire forms: manage_script handler_set takes kind a string, connection a
// string, personas a JSON array of strings (empty), everyone and approve JSON
// booleans; POST /api/v1/portal/scripts/{id}/handlers takes a JSON object
// with the same keys.

// mainOnly2650 defines main() and nothing a policy or transform calls.
const mainOnly2650 = `def main():
    """Sends the weekly summary."""
    print("sent")
`

// TestIssue2650_AScriptWithoutTheEntryIsRefused: a policy and a transform on
// a script defining only main() are refused from manage_script and from the
// portal, naming the version and the entry; a watch, which runs main(), is
// bound.
func TestIssue2650_AScriptWithoutTheEntryIsRefused(t *testing.T) {
	admin := connect(t)
	name := handlerNonce("acc-2650-mailer")
	saveHandlerScript(t, admin, name, mainOnly2650, "main")
	for kind, entry := range map[string]string{"policy": "decide(call)", "transform": "transform(call, result)"} {
		_, refusal := admin.handlerSetRaw(name, kind, map[string]any{"connection": handlerConnection, "everyone": true, "approve": true})
		if !strings.Contains(refusal, "of script "+name+" cannot be a "+kind) || !strings.Contains(refusal, entry) {
			t.Errorf("manage_script bound a %s to a script with no %s: %s", kind, entry, refusal)
		}
	}
	id := issue2179ScriptID(t, admin, name)
	status, body := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/handlers",
		jsonBody(t, map[string]any{"kind": "policy", "connection": handlerConnection, "everyone": true, "approve": true}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(body), "decide(call)") {
		t.Errorf("the portal route bound a policy to a script with no decide(call): HTTP %d %v", status, body)
	}
	watch := bindHandler(t, admin, name, "", "watch", nil)
	if idOf(watch) == "" {
		t.Errorf("a watch on a script defining main() was not bound: %v", watch)
	}
}

// TestIssue2650_EveryoneIsAChoice: a new policy with no personas and no
// people, or an empty persona list, is refused saying to set everyone; with
// everyone it is bound and reported as everyone's.
func TestIssue2650_EveryoneIsAChoice(t *testing.T) {
	admin := connect(t)
	name := handlerNonce("acc-2650-allow")
	saveHandlerScript(t, admin, name, allowPolicy, "decide")
	for label, args := range map[string]map[string]any{
		"no list":    {"connection": handlerConnection, "approve": true},
		"empty list": {"connection": handlerConnection, "personas": []string{}, "approve": true},
	} {
		_, refusal := admin.handlerSetRaw(name, "policy", args)
		if !strings.Contains(refusal, "set everyone to true") {
			t.Errorf("%s: a policy naming no one was bound: %s", label, refusal)
		}
	}
	id := issue2179ScriptID(t, admin, name)
	status, body := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/handlers",
		jsonBody(t, map[string]any{"kind": "policy", "connection": handlerConnection, "personas": []string{}, "approve": true}))
	if status != http.StatusBadRequest {
		t.Errorf("the portal route bound a policy naming no one: HTTP %d %v", status, body)
	}
	h := bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"GET /v1/fixed/acc-2650"}})
	if h["everyone"] != true {
		t.Errorf("a policy bound for everyone does not say so: %v", h)
	}
}
