//go:build integration

package acceptance

import "testing"

// Issue #1606, the caller the model-context budget does not apply to. The
// budget exists to keep a tool result readable by a model. A managed script
// has no model in it: it parses the response in code, so a body cut to a
// prefix string is not parseable, and the steer to api_export is not something
// a run can act on mid-script. A run therefore reads to the connection's cap
// and receives its response whole, the same exemption enrichment already makes
// for a script caller.
//
// Wire forms: every parameter this file touches is typed and admits one JSON
// form -- manage_script's command, name, description, source and state_action,
// run_script's name and wait_seconds. The script's own call sends
// api_invoke_endpoint.query_params.bytes as a number, the form Starlark
// produces for an integer.

// scriptReadsAWholeResponse1606 calls the fixture for a response far past the
// default budget and records what it received. The fixture answers with a JSON
// object carrying the sized content, so a run that was held to the budget
// would see a cut prefix string where this reads a parsed object: indexing
// into it at all proves the body arrived whole and parseable, and its length
// proves no bytes were dropped.
const scriptReadsAWholeResponse1606 = `
res = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/v1/sized",
    "query_params": {"bytes": 200000},
    "purpose": "Acceptance #1606: a run reads a whole response, not one cut to a model's budget.",
})
platform.save_state({
    "content_len": str(len(res["body"]["body"])),
    "body_bytes": str(res["body_bytes"]),
    "truncated": str(res.get("body_truncated", False)),
})
`

func TestIssue1606_AScriptRunReceivesTheWholeResponse(t *testing.T) {
	c := connect(t)
	const name = "acceptance-1606-whole"
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1606: a run is not held to the model-context budget.",
		"source":      scriptReadsAWholeResponse1606,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("run did not succeed: %v", run)
	}

	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	if state["truncated"] != "False" {
		t.Errorf("truncated = %v; want the run's response returned whole", state["truncated"])
	}
	if state["content_len"] != "200000" {
		t.Errorf("content_len = %v; want the whole 200000 bytes the fixture sent, not a body cut to the model-context budget", state["content_len"])
	}
	if state["body_bytes"] != "200026" {
		t.Errorf("body_bytes = %v; want the whole response read rather than stopped at the budget", state["body_bytes"])
	}
}

// TestIssue1606_AModelIsStillHeldToTheBudget is the control for the test
// above: the same endpoint, the same connection, called by this client rather
// than by a run, is cut at the budget and steered to api_export. Without it a
// run receiving the whole response would prove nothing about the exemption --
// it could equally mean the budget stopped working for everyone.
func TestIssue1606_AModelIsStillHeldToTheBudget(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", issue1606SizedArgs(200000))
	assertCutAtInlineBudget(t, out, issue1606Budget)
}
