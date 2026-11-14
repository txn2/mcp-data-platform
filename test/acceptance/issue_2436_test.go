//go:build integration

package acceptance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2436: a policy handler, a script whose decide(call) says
// whether a tool call may run, and what every tool-call handler shares (the
// administrator who creates and approves it, the version that runs, the match,
// the counter, the failure record and the test panel). Every call goes through
// the real client to the api-test fixture, whose own record of each request
// says whether the upstream received it.
//
// Wire forms: manage_script handler_set takes kind "policy", connection a
// string, operations, personas and users JSON arrays of strings, args a JSON
// object, timeout_ms a JSON number, on_failure and items_path strings, approve
// a JSON boolean; handler_test takes connection and as_user strings and
// arguments a JSON object; api_invoke_endpoint's body is sent as a JSON object
// and as a string of JSON, which give the same count and the same decision.

// itemLimitPolicy holds a call carrying more than three items.
const itemLimitPolicy = `def decide(call):
    """Holds a call carrying more than three items."""
    if call["items"] > 3:
        return platform.require_approval(["administrators"], "%d items in one call; the limit is 3." % call["items"])
    return platform.allow()
`

// allowPolicy allows every call.
const allowPolicy = `def decide(_call):
    """Allows every call."""
    return platform.allow()
`

// denyPolicy refuses every call with a reason.
const denyPolicy = `def decide(_call):
    """Refuses every call."""
    return platform.deny("Nothing runs on Fridays.")
`

// approvalPolicy holds every call for the administrators.
const approvalPolicy = `def decide(_call):
    """Holds every call for an administrator."""
    return platform.require_approval(["administrators"], "Every call waits for an administrator.")
`

// raisingPolicy fails on every call.
const raisingPolicy = `def decide(_call):
    """Fails on every call."""
    fail("the policy could not decide")
`

// recordPolicy allows a write only for an account whose record, read from an
// operation the caller's persona may not call, has a hash starting with 2.
const recordPolicy = `def decide(call):
    """Allows the write when the account's record qualifies."""
    account = call["body"]["account"]
    rec = platform.call("api_invoke_endpoint", {"connection": "api-test-fixture", "method": "GET", "path": "/v1/fixed/" + account})
    if rec["body"]["hash"].startswith("2"):
        return platform.allow()
    return platform.deny("Account " + account + " does not qualify.")
`

// recordSamples are the calls a record policy is drafted on: one account
// that qualifies, one that does not.
var recordSamples = []string{sampleWith(`{"account": "hello"}`), sampleWith(`{"account": "world"}`)}

// itemSamples are the calls an item limit is drafted on: under and over it.
var itemSamples = []string{sampleCall, strings.Replace(sampleCall, `"items": 2`, `"items": 5`, 1)}

// writingPolicy tries to write before it decides.
const writingPolicy = `def decide(call):
    """Tries a write, which a policy may not make."""
    platform.call("api_invoke_endpoint", {"connection": "api-test-fixture", "method": "POST", "path": "/v1/echo", "headers": {"X-Trace-Id": call["arguments"]["headers"]["X-Trace-Id"] + "-policy"}, "body": {"from": "policy"}})
    return platform.allow()
`

// printingPolicy prints the call it was given and fails, so a failure record
// holds the call and the print.
const printingPolicy = `def decide(call):
    """Prints the call, then fails."""
    print(call)
    fail("printed")
`

// TestIssue2436_TheRateLimitExampleHoldsTheFourthCall: the built-in rate-limit
// example at 3 calls an hour on one operation lets three calls through and
// holds the fourth, naming the policy and who can approve it.
func TestIssue2436_TheRateLimitExampleHoldsTheFourthCall(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	example := admin.call("manage_script", map[string]any{"command": "get", "name": "policy-rate-limit"})
	source, _ := example["source"].(string)
	if !strings.Contains(source, "def decide(") {
		t.Fatalf("the built-in policy-rate-limit example has no decide(call): %v", example)
	}
	name := handlerNonce("acc-2436-rate")
	if out := admin.call("manage_script", map[string]any{"command": "create", "name": name, "source": source, "params": example["params"]}); out["status"] != "created" {
		t.Fatalf("the example was not saved as a script: %v", out)
	}
	bindHandler(t, admin, name, "", "policy", map[string]any{
		"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail},
		"args": map[string]any{"limit": 3, "window": "1h", "approvers": []string{"administrators"}},
	})
	trace := handlerNonce("t2436-rate")
	for i := 1; i <= 3; i++ {
		if res, text := echoPut(t, owner, trace, map[string]any{"n": i}); res.IsError {
			t.Fatalf("call %d of 3 was refused: %s", i, text)
		}
	}
	res, text := echoPut(t, owner, trace, map[string]any{"n": 4})
	if codeOf(res) != "APPROVAL_REQUIRED" {
		t.Fatalf("the 4th call was not held: %s", text)
	}
	msg := fmt.Sprint(handlerError(res)["message"])
	if !strings.Contains(msg, name) || !strings.Contains(msg, "an administrator can approve it") {
		t.Errorf("the hold does not name the policy %s and its approver: %s", name, msg)
	}
}

// TestIssue2436_APolicyReadsWhatTheCallersPersonaCannot: a policy reads a
// record from an operation outside the caller's persona's API routes, allows
// the call when the record qualifies and refuses it when it does not; the
// caller still cannot call that operation.
func TestIssue2436_APolicyReadsWhatTheCallersPersonaCannot(t *testing.T) {
	admin := connect(t)
	persona, caller, _ := tempPersona(t, admin, "acc-2436-routes", []any{map[string]any{"connection": handlerConnection, "paths": []string{"/v1/echo"}}})

	name := handlerNonce("acc-2436-record")
	saveHandlerScript(t, admin, name, recordPolicy, "decide", recordSamples...)
	bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "personas": []string{persona}})

	trace := handlerNonce("t2436-record")
	if res, text := echoPut(t, caller, trace, map[string]any{"account": "hello"}); res.IsError {
		t.Errorf("a qualifying account was refused: %s", text)
	}
	res, text := echoPut(t, caller, trace, map[string]any{"account": "world"})
	if codeOf(res) != "POLICY_DENIED" || !strings.Contains(text, "Account world does not qualify.") {
		t.Errorf("an account that does not qualify was not refused with the policy's reason: %s", text)
	}
	direct, dtext, err := caller.callRaw("api_invoke_endpoint", map[string]any{"purpose": handlerPurpose, "connection": handlerConnection, "method": "GET", "path": "/v1/fixed/hello"})
	if err != nil || !direct.IsError {
		t.Errorf("the caller reached the record the policy read, outside their persona's routes: %s", dtext)
	}
}

// TestIssue2436_AnItemLimitHoldsTheCallBeforeItIsSent: one call whose body
// holds 5 items against a limit of 3 is held before it is sent, in both forms
// the body admits; the upstream receives nothing.
func TestIssue2436_AnItemLimitHoldsTheCallBeforeItIsSent(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2436-items")
	saveHandlerScript(t, admin, name, itemLimitPolicy, "decide", itemSamples...)
	bindHandler(t, admin, name, "", "policy", map[string]any{
		"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}, "items_path": "subscribers",
	})
	since := time.Now()
	for form, body := range bodyForms(t, subscribers(5)) {
		trace := handlerNonce("t2436-items-" + form)
		res, text := echoPut(t, owner, trace, body)
		if codeOf(res) != "APPROVAL_REQUIRED" || !strings.Contains(text, "5 items in one call") {
			t.Errorf("body as %s: 5 items were not held with the count: %s", form, text)
		}
		if n := fixtureReceived(t, since, trace); n != 0 {
			t.Errorf("body as %s: the upstream received %d requests for a held call", form, n)
		}
	}
	for form, body := range bodyForms(t, subscribers(2)) {
		if res, text := echoPut(t, owner, handlerNonce("t2436-under-"+form), body); res.IsError {
			t.Errorf("body as %s: 2 items were refused: %s", form, text)
		}
	}
}

// TestIssue2436_APolicyThatRaisesHoldsOrRefuses: a policy that fails holds
// the call; set to refuse on failure, it refuses it.
func TestIssue2436_APolicyThatRaisesHoldsOrRefuses(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2436-raise")
	saveHandlerScript(t, admin, name, raisingPolicy, "decide")
	h := bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	if res, text := echoPut(t, owner, handlerNonce("t2436-raise"), map[string]any{}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Errorf("a failing policy did not hold the call: %s", text)
	}
	admin.handlerSet(name, "policy", map[string]any{"handler": idOf(h), "on_failure": "deny"})
	if res, text := echoPut(t, owner, handlerNonce("t2436-raise-deny"), map[string]any{}); codeOf(res) != "POLICY_DENIED" {
		t.Errorf("a failing policy set to refuse did not refuse the call: %s", text)
	}
}

// TestIssue2436_ADenyBeatsARequireApproval: a deny and a require-approval
// policy on one call refuse it with the deny's reason.
func TestIssue2436_ADenyBeatsARequireApproval(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	held, denied := handlerNonce("acc-2436-held"), handlerNonce("acc-2436-deny")
	saveHandlerScript(t, admin, held, approvalPolicy, "decide")
	saveHandlerScript(t, admin, denied, denyPolicy, "decide")
	for _, n := range []string{held, denied} {
		bindHandler(t, admin, n, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	}
	res, text := echoPut(t, owner, handlerNonce("t2436-both"), map[string]any{})
	if codeOf(res) != "POLICY_DENIED" || !strings.Contains(text, "Nothing runs on Fridays.") {
		t.Errorf("a deny beside a require-approval did not refuse with the deny's reason: %s", text)
	}
}

// TestIssue2436_OnlyAnAdministratorCreatesOrApprovesAPolicy: a non-administrator
// creating a policy, changing its match, or approving its version is refused.
func TestIssue2436_OnlyAnAdministratorCreatesOrApprovesAPolicy(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2436-who")
	saveHandlerScript(t, owner, name, allowPolicy, "decide")
	if out, text := owner.handlerSetRaw(name, "policy", map[string]any{"connection": handlerConnection, "operations": []string{"PUT /v1/echo"}}); out != nil {
		t.Fatalf("a non-administrator created a policy: %v", out)
	} else if !strings.Contains(text, "administrator") {
		t.Errorf("the refusal does not say an administrator creates a policy: %s", text)
	}
	h := bindHandler(t, admin, name, devOwnerEmail, "policy", map[string]any{"operations": []string{"PUT /v1/echo"}})
	if out, text := owner.handlerSetRaw(name, "policy", map[string]any{"handler": idOf(h), "operations": []string{"*"}}); out != nil {
		t.Errorf("a non-administrator changed a policy's match: %v", out)
	} else if !strings.Contains(text, "administrator") {
		t.Errorf("the refusal does not say an administrator changes a policy: %s", text)
	}
	owner.saveEdit(map[string]any{"command": "update", "name": name, "source": drafted(denyPolicy, "decide")}, nil)
	req := pendingHandlerApproval(t, admin, name)
	res, text, _ := owner.callRaw("manage_approval", map[string]any{"command": "decide", "id": req, "decision": "approve"})
	if res == nil || !res.IsError {
		t.Errorf("a non-administrator approved a policy's version: %s", text)
	}
}

// TestIssue2436_ANonAdministratorsVersionWaitsForApproval: a non-administrator's
// new version of a policy script does not run until an administrator approves
// it, and the approved version keeps deciding meanwhile. Approved with
// manage_approval decide, it runs with the deciding administrator's roles:
// its read through the platform answers, so it denies rather than failing
// and holding the call.
func TestIssue2436_ANonAdministratorsVersionWaitsForApproval(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := handlerNonce("acc-2436-wait")
	saveHandlerScript(t, owner, name, allowPolicy, "decide")
	bindHandler(t, admin, name, devOwnerEmail, "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devPeerEmailAddr}})
	owner.saveEdit(map[string]any{"command": "update", "name": name, "source": drafted(recordPolicy, "decide", recordSamples...)}, nil)
	world := map[string]any{"account": "world"}
	if res, text := echoPut(t, peer, handlerNonce("t2436-wait"), world); res.IsError {
		t.Fatalf("the unapproved version decided, refusing the call: %s", text)
	}
	req := pendingHandlerApproval(t, admin, name)
	admin.call("manage_approval", map[string]any{"command": "decide", "id": req, "decision": "approve"})
	awaitCodeFor(t, peer, world, "POLICY_DENIED", "the approved version did not take over with its approver's roles")
}

// TestIssue2436_APolicyScriptCannotWriteAndItsReadsAreNotEvaluated: a policy
// script calling a write tool is refused (the call is held and the write never
// reaches the upstream); the policy's own reads are not evaluated by policies.
func TestIssue2436_APolicyScriptCannotWriteAndItsReadsAreNotEvaluated(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	writer := handlerNonce("acc-2436-write")
	saveHandlerScript(t, admin, writer, writingPolicy, "decide")
	h := bindHandler(t, admin, writer, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	since := time.Now()
	trace := handlerNonce("t2436-write")
	if res, text := echoPut(t, owner, trace, map[string]any{}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Errorf("a policy whose write was refused did not hold the call: %s", text)
	}
	if n := fixtureReceived(t, since, trace+"-policy"); n != 0 {
		t.Errorf("the policy's write reached the upstream %d times", n)
	}
	got := admin.call("manage_script", map[string]any{"command": "handler_get", "handler": idOf(h)})
	if raw, _ := json.Marshal(got["recent_failures"]); !strings.Contains(string(raw), "write") {
		t.Errorf("the failure record does not say the write was refused: %s", raw)
	}
	admin.handlerOff(h["handler"])

	reader, blocker := handlerNonce("acc-2436-read"), handlerNonce("acc-2436-block")
	saveHandlerScript(t, admin, reader, recordPolicy, "decide", recordSamples...)
	saveHandlerScript(t, admin, blocker, denyPolicy, "decide")
	bindHandler(t, admin, reader, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	bindHandler(t, admin, blocker, "", "policy", map[string]any{"operations": []string{"GET /v1/fixed/*"}})
	if res, text := echoPut(t, owner, handlerNonce("t2436-read"), map[string]any{"account": "hello"}); res.IsError {
		t.Errorf("a policy's own read was evaluated by another policy: %s", text)
	}
}

// TestIssue2436_TheOwnerCannotReadThePolicysRuns: a policy's failure record
// (what it was given and printed) is an administrator's; the script's owner,
// who is not one, reads none of it.
func TestIssue2436_TheOwnerCannotReadThePolicysRuns(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := handlerNonce("acc-2436-runs")
	saveHandlerScript(t, owner, name, printingPolicy, "decide")
	h := bindHandler(t, admin, name, devOwnerEmail, "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devPeerEmailAddr}})
	marker := handlerNonce("peer-arg")
	echoPut(t, peer, handlerNonce("t2436-runs"), map[string]any{"marker": marker})
	failures := awaitFailures(t, admin, idOf(h))
	if !strings.Contains(failures, marker) {
		t.Fatalf("the administrator's failure record lacks the call's argument: %s", failures)
	}
	ownGet := owner.call("manage_script", map[string]any{"command": "handler_get", "handler": idOf(h)})
	if raw, _ := json.Marshal(ownGet); strings.Contains(string(raw), marker) {
		t.Errorf("the owner read the call a policy was given: %s", raw)
	}
	if status, body := owner.rest(http.MethodGet, "/api/v1/portal/handlers/"+idOf(h)+"/failures", nil); status != http.StatusNotFound {
		t.Errorf("the owner's failures read answered %d, want 404: %v", status, body)
	}
	runs := owner.call("manage_script", map[string]any{"command": "runs", "name": name})
	if raw, _ := json.Marshal(runs); strings.Contains(string(raw), marker) {
		t.Errorf("the owner's run list carries a policy's input: %s", raw)
	}
}

// TestIssue2436_AStoredSecretReachesNoSink: a {{secret:<name>}} value used in
// a matched call appears in no policy print, failure record, audit row, log or
// span, searched as written, base64, hex, URL- and JSON-escaped.
func TestIssue2436_AStoredSecretReachesNoSink(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	secretName := handlerNonce("acc-2436-secret")
	value := handlerNonce("SeCrEt-2436-value")
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secretName, map[string]any{
		"description": "Acceptance #2436.", "value": value, "allow_connections": []string{handlerConnection}, "allow_personas": []string{"collaborator"},
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secretName, nil) })
	name := handlerNonce("acc-2436-sink")
	saveHandlerScript(t, admin, name, printingPolicy, "decide")
	h := bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	trace := handlerNonce("t2436-sink")
	res, _, err := owner.callRaw("api_invoke_endpoint", map[string]any{
		"purpose":    handlerPurpose,
		"connection": handlerConnection, "method": "PUT", "path": "/v1/echo",
		"headers": map[string]any{handlerTraceHeader: trace, "X-Account-Key": "{{secret:" + secretName + "}}"},
		"body":    map[string]any{"key": "{{secret:" + secretName + "}}"},
	})
	if err != nil || codeOf(res) != "APPROVAL_REQUIRED" {
		t.Fatalf("the call with a failing policy was not held: %v %v", err, firstText(res))
	}
	failures := awaitFailures(t, admin, idOf(h))
	if !strings.Contains(failures, "{{secret:"+secretName+"}}") {
		t.Errorf("the policy was not given the placeholder: %s", failures)
	}
	sinks := map[string]string{"failure record": failures}
	rows := admin.list("/api/v1/admin/audit/events?session_id=" + owner.sessionID + "&per_page=50")
	raw, _ := json.Marshal(rows)
	sinks["audit rows"] = string(raw)
	sinks["logs and spans"] = devSinkText(t)
	for sink, text := range sinks {
		for _, form := range secretForms(value) {
			if strings.Contains(text, form) {
				t.Errorf("the secret's value reached the %s as %q", sink, form)
			}
		}
	}
}

// TestIssue2436_TheTestPanelMatchesTheRealCall: handler_test gives the outcome
// the real call gets, for an allowed, a refused and a held call.
func TestIssue2436_TheTestPanelMatchesTheRealCall(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2436-panel")
	saveHandlerScript(t, admin, name, recordPolicy, "decide", recordSamples...)
	items := handlerNonce("acc-2436-panel-items")
	saveHandlerScript(t, admin, items, itemLimitPolicy, "decide", itemSamples...)
	bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	bindHandler(t, admin, items, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}, "items_path": "subscribers"})
	cases := []struct {
		label string
		body  map[string]any
		want  string
		code  string
	}{
		{"allowed", map[string]any{"account": "hello"}, "runs", ""},
		{"refused", map[string]any{"account": "world"}, "denied", "POLICY_DENIED"},
		{"held", map[string]any{"account": "hello", "subscribers": subscribers(5)["subscribers"]}, "held", "APPROVAL_REQUIRED"},
	}
	for _, tc := range cases {
		panel := handlerTest(t, admin, map[string]any{
			"as_user": devOwnerEmail, "tool": "api_invoke_endpoint",
			"arguments": map[string]any{"method": "PUT", "path": "/v1/echo", "body": tc.body},
		})
		if panel["outcome"] != tc.want {
			t.Errorf("%s: the test panel says %v, want %s: %v", tc.label, panel["outcome"], tc.want, panel)
		}
		res, text := echoPut(t, owner, handlerNonce("t2436-panel"), tc.body)
		if codeOf(res) != tc.code {
			t.Errorf("%s: the real call answered %q, the panel %v: %s", tc.label, codeOf(res), panel["outcome"], text)
		}
	}
}

// TestIssue2436_AnUnmatchedCallIsNoSlower: an unmatched tool call's p50 and
// p99 over 1,000 calls, with 50 policies bound to other connections, are
// within 5% of the same calls with none bound, on the same server under the
// same limits. The no-handler runs before and after are averaged, so drift in
// the stack over the run is not read as the handlers' cost.
func TestIssue2436_AnUnmatchedCallIsNoSlower(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	unmatchedLatency(t, admin, owner, "acc-2436-latency", "policy", allowPolicy, "decide")
}

// pendingHandlerApproval is the id of the pending call_handler request for
// the latest version of the script name.
func pendingHandlerApproval(t *testing.T, admin *client, name string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		out := admin.call("manage_approval", map[string]any{"command": "list", "status": "pending", "purpose": "call_handler"})
		for _, r := range listOf(out, "requests") {
			if r["script_name"] == name {
				return fmt.Sprint(r["id"])
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pending call_handler request for %s: %v", name, out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// awaitCode makes a call as c until it answers with code, bounded, since a
// replica reads a changed handler on its next refresh.
func awaitCode(t *testing.T, c *client, code, what string) {
	t.Helper()
	awaitCodeFor(t, c, map[string]any{}, code, what)
}

// awaitCodeFor is awaitCode for a call carrying body.
func awaitCodeFor(t *testing.T, c *client, body map[string]any, code, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, text := echoPut(t, c, handlerNonce("t-await"), body)
		if codeOf(res) == code {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the call answered %q, want %s: %s", what, codeOf(res), code, text)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// awaitFailures reads a handler's failure record as an administrator, waiting
// for one to be written.
func awaitFailures(t *testing.T, admin *client, handler string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		status, body := admin.rest(http.MethodGet, "/api/v1/portal/handlers/"+handler+"/failures", nil)
		raw, _ := json.Marshal(body)
		if status == http.StatusOK && strings.Contains(string(raw), "\"arguments\"") {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure recorded for handler %s: %d %s", handler, status, raw)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// devSinkText is what the dev replicas have logged and what the collector has
// written of their logs and spans.
func devSinkText(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	all.WriteString(readLogs2546(t))
	for _, file := range []string{"logs.jsonl", "traces.jsonl"} {
		f, err := os.Open(otelSignalFile(file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			all.Write(sc.Bytes())
			all.WriteByte('\n')
		}
		_ = f.Close()
	}
	return all.String()
}

// TestIssue2436_APolicyDecidesEveryApplicationDatabaseStatement: a policy on
// an application database refuses a script's statement however it is sent
// (in platform.transaction, in platform.batch, through platform.query_iter),
// and the refused write leaves no row.
func TestIssue2436_APolicyDecidesEveryApplicationDatabaseStatement(t *testing.T) {
	admin := connect(t)
	persona, covered, _ := tempPersona(t, admin, "acc-2436-appdb", nil)
	schema := shop2402(t, admin, "policy")
	name := handlerNonce("acc-2436-appdb")
	saveHandlerScript(t, admin, name, denyPolicy, "decide")
	bindHandler(t, admin, name, "", "policy", map[string]any{"connection": conn2402, "personas": []string{persona}})
	insert := "INSERT INTO " + schema + ".orders (id, total) VALUES (1, 10)"
	for label, body := range map[string]string{
		"a transaction": fmt.Sprintf(`
    def write(tx):
        """Writes in the transaction."""
        tx.execute(%q)

    platform.transaction(%q, write)`, insert, conn2402),
		"a batch":        fmt.Sprintf(`platform.batch(connection = %q, statements = [%q])`, conn2402, insert),
		"query_iter":     fmt.Sprintf(`[r for r in platform.query_iter(connection = %q, sql = "SELECT id FROM %s.orders")]`, conn2402, schema),
		"platform.query": fmt.Sprintf(`platform.query(connection = %q, sql = "SELECT id FROM %s.orders")`, conn2402, schema),
	} {
		out := draft2428(covered, "acc-2436-appdb", "\ndef main():\n    \"\"\"Sends a statement.\"\"\"\n    "+strings.TrimSpace(body)+"\n", true)
		if text := asJSON(out); out["status"] == "succeeded" || !strings.Contains(text, "Nothing runs on Fridays.") {
			t.Errorf("the policy did not refuse a statement in %s: %s", label, text)
		}
	}
	if n := count2402(t, admin, schema, "orders"); n != "0" {
		t.Errorf("orders holds %s rows after the refused writes, want 0", n)
	}
}

// TestIssue2436_APolicyQueuesAMessageSentAfterTheCall: a policy's
// platform.notify is not sent from its read-only run but queued, and sent once
// the call it held has been answered; the call is held all the same.
func TestIssue2436_APolicyQueuesAMessageSentAfterTheCall(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	channel := handlerNonce("acc-2436-notice")
	putChannel(t, admin, channel, map[string]any{"kind": "email", "recipients": []string{"acc-2436-ops@example.com"}})
	t.Cleanup(func() { deleteChannel(t, admin, channel) })
	marker := handlerNonce("acc-2436 held a call")
	name := handlerNonce("acc-2436-notice")
	saveHandlerScript(t, admin, name, fmt.Sprintf(`def decide(call):
    """Holds every call and tells the operators."""
    platform.notify(%q, %q, body = call["operation"])
    return platform.require_approval(["administrators"], "Every call waits.")
`, channel, marker), "decide")
	bindHandler(t, admin, name, "", "policy", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	if res, text := echoPut(t, owner, handlerNonce("t2436-notice"), map[string]any{}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Fatalf("the policy did not hold the call: %s", text)
	}
	waitFor(t, "the policy's message", func() (bool, string) {
		subjects := mailpitMessages(t)
		return containing(subjects, marker), fmt.Sprint(len(subjects), " messages")
	})
}
