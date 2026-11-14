//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance for #2437: the queue a held call goes to. A held call asks the
// person, where their client can be asked, whether to request approval; where
// it cannot, the call answers APPROVAL_REQUIRED and manage_approval submit
// files the request. An approved request is an allowance: a number of calls of
// one operation, for one person, on one connection, until a deadline. Its
// approvers are the ones the policy names, or an administrator.
//
// Wire forms: the held call's api_invoke_endpoint body is sent as a JSON object
// and as a string of JSON; manage_approval submit takes kind "call", tool a
// string, arguments a JSON object, calls a JSON number and reason a string;
// decide takes id and decision strings, calls a JSON number and until an RFC
// 3339 string; an elicitation answer carries calls as a JSON number and reason
// as a string.

// namedApproverPolicy holds every call for the peer to approve.
const namedApproverPolicy = `def decide(_call):
    """Holds every call for asset.peer@example.com."""
    return platform.require_approval(["asset.peer@example.com"], "Every call waits for the peer.")
`

// elicitingClient is a session whose client declares elicitation and answers
// each prompt with action and content, recording each prompt's message.
type elicitingClient struct {
	*client
	mu      sync.Mutex
	asked   []string
	action  string
	content map[string]any
}

func connectEliciting(t *testing.T, apiKey, protocolVersion string) *elicitingClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ec := &elicitingClient{action: "decline"}
	httpClient := &http.Client{Transport: authRoundTripper{key: apiKey, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance-2437", Version: "1.0.0"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			ec.mu.Lock()
			defer ec.mu.Unlock()
			ec.asked = append(ec.asked, req.Params.Message)
			return &mcp.ElicitResult{Action: ec.action, Content: ec.content}, nil
		},
	})
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: httpClient},
		&mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		t.Fatalf("no platform answers at %s (%v); start one with `make dev`", baseURL(), err)
	}
	t.Cleanup(func() { _ = session.Close() })
	c := &client{t: t, ctx: ctx, session: session, base: baseURL(), apiKey: apiKey}
	info := c.call("platform_info", nil)
	c.info = info
	c.sessionID, _ = info["session_id"].(string)
	c.call("search", map[string]any{
		"intent": "acceptance suite discovery", "limit": 1,
		"purpose": "The acceptance suite performs the discovery the search-first gate requires.",
	})
	ec.client = c
	return ec
}

func (ec *elicitingClient) answer(action string, content map[string]any) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.action, ec.content = action, content
}

func (ec *elicitingClient) prompts() []string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return append([]string(nil), ec.asked...)
}

// heldOperation binds a policy holding every PUT /v1/echo by user for the
// approvers source names, and returns the script's name.
func heldOperation(t *testing.T, admin *client, user, source string) string {
	t.Helper()
	return heldFor(t, admin, map[string]any{"users": []string{user}}, source)
}

// heldFor binds a policy holding every PUT /v1/echo by the callers who match
// for the approvers source names, and returns the script's name.
func heldFor(t *testing.T, admin *client, who map[string]any, source string) string {
	t.Helper()
	name := handlerNonce("acc-2437-held")
	saveHandlerScript(t, admin, name, source, "decide")
	settings := map[string]any{"operations": []string{"PUT /v1/echo"}}
	for k, v := range who {
		settings[k] = v
	}
	bindHandler(t, admin, name, "", "policy", settings)
	return name
}

// callRequests is every request of kind call that requester filed since since,
// as an administrator lists them.
func callRequests(t *testing.T, admin *client, requester string, since time.Time) []map[string]any {
	t.Helper()
	out := admin.call("manage_approval", map[string]any{"command": "list", "kind": "call"})
	var mine []map[string]any
	for _, r := range listOf(out, "requests") {
		created, _ := time.Parse(time.RFC3339, fmt.Sprint(r["created_at"]))
		if r["requester_email"] == requester && !created.Before(since.Add(-time.Second)) {
			mine = append(mine, r)
		}
	}
	return mine
}

// approvalNotices counts, by recipient, the notices of a held call's request
// sent since since: the admin history names what a request is about in its
// item title, and a held call's ends "which a policy held".
func approvalNotices(t *testing.T, admin *client, since time.Time) map[string]int {
	t.Helper()
	status, body := admin.rest(http.MethodGet, "/api/v1/admin/notifications?per_page=200", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: %d %v", status, body)
	}
	counts := map[string]int{}
	rows, _ := body["data"].([]any)
	for _, row := range rows {
		m, _ := row.(map[string]any)
		created, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(m["created_at"]))
		if strings.HasSuffix(fmt.Sprint(m["item_title"]), "which a policy held") && !created.Before(since.Add(-time.Second)) {
			counts[fmt.Sprint(m["recipient"])]++
		}
	}
	return counts
}

// TestIssue2437_AHeldCallAsksThePerson: where the client can be asked, a held
// call asks; accepting files one request and notifies each approver once;
// declining files nothing; no answer files nothing and the call is audited
// input_required. Accepting never lets the call run.
func TestIssue2437_AHeldCallAsksThePerson(t *testing.T) {
	admin := connect(t)
	heldOperation(t, admin, devOwnerEmail, namedApproverPolicy)
	ec := connectEliciting(t, devOwnerAPIKey, "2026-07-28")

	since := time.Now()
	trace := handlerNonce("t2437-accept")
	ec.answer("accept", map[string]any{"calls": 1, "reason": "Weekly import."})
	res, text := echoPut(t, ec.client, trace, map[string]any{"n": 1})
	if len(ec.prompts()) == 0 {
		t.Fatalf("a held call did not ask the person: %s (isError=%v)", text, res.IsError)
	}
	if !res.IsError || fixtureReceived(t, since, trace) != 0 {
		t.Errorf("accepting the prompt let the call run: %s", text)
	}
	filed := callRequests(t, admin, devOwnerEmail, since)
	if len(filed) != 1 {
		t.Fatalf("accepting filed %d requests, want 1: %v", len(filed), filed)
	}
	id := fmt.Sprint(filed[0]["id"])
	if !strings.Contains(text, id) {
		t.Errorf("the answer does not name the request it filed (%s): %s", id, text)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		notices := approvalNotices(t, admin, since)
		if notices[devPeerEmailAddr] == 1 {
			for to, n := range notices {
				if n != 1 {
					t.Errorf("%s was notified %d times", to, n)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the named approver was not notified once: %v", notices)
		}
		time.Sleep(250 * time.Millisecond)
	}

	filedBefore := map[string]bool{}
	for _, r := range callRequests(t, admin, devOwnerEmail, time.Time{}) {
		filedBefore[fmt.Sprint(r["id"])] = true
	}
	ec.answer("decline", nil)
	echoPut(t, ec.client, handlerNonce("t2437-decline"), map[string]any{"n": 2})
	ec.answer("cancel", nil)
	_, unanswered := echoPut(t, ec.client, handlerNonce("t2437-cancel"), map[string]any{"n": 3})
	for _, r := range callRequests(t, admin, devOwnerEmail, time.Time{}) {
		if !filedBefore[fmt.Sprint(r["id"])] {
			t.Errorf("declining or not answering filed a request: %v", r["id"])
		}
	}
	if !strings.Contains(unanswered, "not answered") {
		t.Errorf("an unanswered prompt does not say so: %s", unanswered)
	}
	rows := admin.list("/api/v1/admin/audit/events?session_id=" + ec.sessionID + "&tool_name=api_invoke_endpoint&per_page=20")
	raw, _ := json.Marshal(rows)
	if !strings.Contains(string(raw), "input_required") {
		t.Errorf("no call of the session is audited input_required: %s", raw)
	}
}

// TestIssue2437_WhereTheClientCannotBeAskedTheAgentSubmits: a client that
// cannot be asked gets APPROVAL_REQUIRED, and manage_approval submit files the
// request, the body in either form filing the same one.
func TestIssue2437_WhereTheClientCannotBeAskedTheAgentSubmits(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	heldOperation(t, admin, devOwnerEmail, namedApproverPolicy)
	renderings := map[string]string{}
	for form, body := range bodyForms(t, subscribers(2)) {
		res, text := echoPut(t, owner, handlerNonce("t2437-cant"), body)
		if codeOf(res) != "APPROVAL_REQUIRED" {
			t.Fatalf("body as %s: a client that cannot be asked was not told APPROVAL_REQUIRED: %s", form, text)
		}
		out := owner.call("manage_approval", map[string]any{
			"command": "submit", "kind": "call", "tool": "api_invoke_endpoint", "calls": 1, "reason": "Two subscribers.",
			"arguments": map[string]any{"connection": handlerConnection, "method": "PUT", "path": "/v1/echo", "body": body},
		})
		if out["status"] != "pending" || out["kind"] != "call" {
			t.Fatalf("body as %s: submit did not file a pending call request: %v", form, out)
		}
		renderings[form] = fmt.Sprint(out["rendering"])
	}
	if renderings["object"] != renderings["string"] {
		t.Errorf("the two body forms filed different requests:\nobject: %s\nstring: %s", renderings["object"], renderings["string"])
	}
}

// TestIssue2437_AnAllowanceSpendsAndExpires: an allowance approved for 10
// lets 10 more calls through and holds the 11th; an allowance past its
// deadline holds the next call.
func TestIssue2437_AnAllowanceSpendsAndExpires(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	heldOperation(t, admin, devOwnerEmail, approvalPolicy)
	id := submitCall(t, owner, 10)
	admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve", "calls": 10, "until": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	for i := 1; i <= 10; i++ {
		if res, text := echoPut(t, owner, handlerNonce("t2437-spend"), map[string]any{"n": i}); res.IsError {
			t.Fatalf("call %d of the allowance's 10 was refused: %s", i, text)
		}
	}
	if res, text := echoPut(t, owner, handlerNonce("t2437-11"), map[string]any{"n": 11}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Errorf("the 11th call was not held: %s", text)
	}

	id = submitCall(t, owner, 5)
	admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve", "calls": 5, "until": time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)})
	time.Sleep(4 * time.Second)
	if res, text := echoPut(t, owner, handlerNonce("t2437-late"), map[string]any{}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Errorf("a call after the allowance's deadline was not held: %s", text)
	}
}

// TestIssue2437_WhoDecides: a named approver decides; someone neither named
// nor an administrator is refused; a non-administrator deciding their own
// request is refused; an administrator's own approval succeeds, marked
// self-approved. A decided request keeps the platform's rendering of the
// call, not its arguments.
func TestIssue2437_WhoDecides(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	reporter := connectAs(t, analystKey1892)
	heldOperation(t, admin, devOwnerEmail, namedApproverPolicy)
	id := submitCall(t, owner, 1)
	for who, c := range map[string]*client{"neither named nor an administrator": reporter, "the requester": owner} {
		res, text, _ := c.callRaw("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
		if res == nil || !res.IsError {
			t.Errorf("%s decided the request: %s", who, text)
		}
	}
	if out := peer.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"}); out["status"] != "approved" {
		t.Errorf("the named approver could not decide: %v", out)
	}
	if got := admin.call("manage_approval", map[string]any{"command": "get", "id": id}); got["arguments"] != nil || got["rendering"] == "" || got["rendering"] == nil {
		t.Errorf("a decided request still holds the call's arguments, or lost the platform's rendering: %v", got)
	}
	// The one approved call is spent here, so it reaches no other criterion.
	if res, text := echoPut(t, owner, handlerNonce("t2437-spent"), map[string]any{}); res.IsError {
		t.Errorf("the approved call was refused: %s", text)
	}

	heldOperation(t, admin, "admin@example.com", approvalPolicy)
	if res, text := echoPut(t, admin, handlerNonce("t2437-self"), map[string]any{}); codeOf(res) != "APPROVAL_REQUIRED" {
		t.Fatalf("the administrator's own call was not held: %s", text)
	}
	own := submitCall(t, admin, 1)
	out := admin.call("manage_approval", map[string]any{"command": "decide", "id": own, "decision": "approve"})
	if out["status"] != "approved" || out["self_approved"] != true {
		t.Errorf("an administrator's own approval is not marked self-approved: %v", out)
	}
	if res, text := echoPut(t, admin, handlerNonce("t2437-self-spent"), map[string]any{}); res.IsError {
		t.Errorf("the administrator's approved call was refused: %s", text)
	}
}

// TestIssue2437_ASubmittedRequestCannotBeChanged: a filed request's arguments
// cannot be changed through any surface.
func TestIssue2437_ASubmittedRequestCannotBeChanged(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	heldOperation(t, admin, devOwnerEmail, approvalPolicy)
	id := submitCall(t, owner, 1)
	before := owner.call("manage_approval", map[string]any{"command": "get", "id": id})
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		status, _ := owner.rest(method, "/api/v1/portal/approvals/"+url.PathEscape(id), jsonBody(t, map[string]any{"arguments": map[string]any{"body": map[string]any{"changed": true}}}))
		if status/100 == 2 {
			t.Errorf("%s on a request answered %d", method, status)
		}
	}
	res, text, _ := owner.callRaw("manage_approval", map[string]any{
		"command": "submit", "id": id, "kind": "call", "tool": "api_invoke_endpoint",
		"arguments": map[string]any{"connection": handlerConnection, "method": "PUT", "path": "/v1/echo", "body": map[string]any{"changed": true}},
	})
	if res != nil && !res.IsError && strings.Contains(text, id) {
		t.Errorf("a submit naming a filed request changed it: %s", text)
	}
	after := owner.call("manage_approval", map[string]any{"command": "get", "id": id})
	if fmt.Sprint(before["rendering"]) != fmt.Sprint(after["rendering"]) {
		t.Errorf("the request's arguments changed:\nbefore %v\nafter  %v", before["rendering"], after["rendering"])
	}
}

// TestIssue2437_AnAllowanceDoesNotOutliveTheGrant: an allowance whose
// requester lost the connection's grant does not let the call through.
func TestIssue2437_AnAllowanceDoesNotOutliveTheGrant(t *testing.T) {
	admin := connect(t)
	persona, caller, body := tempPersona(t, admin, "acc-2437-grant", nil)
	heldFor(t, admin, map[string]any{"personas": []string{persona}}, approvalPolicy)
	id := submitCall(t, caller, 5)
	admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve", "calls": 5})
	if res, text := echoPut(t, caller, handlerNonce("t2437-granted"), map[string]any{}); res.IsError {
		t.Fatalf("the approved allowance did not let the call through: %s", text)
	}
	body["deny_connections"] = []string{handlerConnection}
	if code := admin.restJSON(http.MethodPut, "/api/v1/admin/personas/"+persona, body); code != http.StatusOK {
		t.Fatalf("taking the connection from %s: HTTP %d", persona, code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, text := echoPut(t, caller, handlerNonce("t2437-grant"), map[string]any{})
		if res.IsError && codeOf(res) != "APPROVAL_REQUIRED" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("an allowance let a call through after its requester lost the grant: %s", text)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue2437_AStoredSecretReachesNoApprovalSink: a {{secret:<name>}} value
// in a held call reaches no approval row, notification, portal response,
// audit row, log or span, searched as written, base64, hex, URL- and
// JSON-escaped.
func TestIssue2437_AStoredSecretReachesNoApprovalSink(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	secretName := handlerNonce("acc-2437-secret")
	value := handlerNonce("SeCrEt-2437-value")
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secretName, map[string]any{
		"description": "Acceptance.", "value": value, "allow_connections": []string{handlerConnection}, "allow_personas": []string{"collaborator"},
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secretName, nil) })
	heldOperation(t, admin, devOwnerEmail, namedApproverPolicy)
	args := map[string]any{
		"connection": handlerConnection, "method": "PUT", "path": "/v1/echo", "purpose": handlerPurpose,
		"headers": map[string]any{"X-Account-Key": "{{secret:" + secretName + "}}"}, "body": map[string]any{"key": "{{secret:" + secretName + "}}"},
	}
	res, _, _ := owner.callRaw("api_invoke_endpoint", args)
	if codeOf(res) != "APPROVAL_REQUIRED" {
		t.Fatalf("the call was not held: %s", firstText(res))
	}
	out := owner.call("manage_approval", map[string]any{"command": "submit", "kind": "call", "tool": "api_invoke_endpoint", "arguments": args, "calls": 1, "reason": "Secret check."})
	id := fmt.Sprint(out["id"])
	sinks := map[string]string{}
	got := admin.call("manage_approval", map[string]any{"command": "get", "id": id})
	raw, _ := json.Marshal(got)
	sinks["the approval row"] = string(raw)
	if !strings.Contains(string(raw), "{{secret:"+secretName+"}}") {
		t.Errorf("the request does not keep the placeholder as sent: %s", raw)
	}
	_, portal := admin.rest(http.MethodGet, "/api/v1/portal/approvals/"+url.PathEscape(id), nil)
	raw, _ = json.Marshal(portal)
	sinks["the portal response"] = string(raw)
	_, notes := admin.rest(http.MethodGet, "/api/v1/admin/notifications?per_page=200", nil)
	raw, _ = json.Marshal(notes)
	sinks["notifications"] = string(raw)
	rows := admin.list("/api/v1/admin/audit/events?session_id=" + owner.sessionID + "&per_page=50")
	raw, _ = json.Marshal(rows)
	sinks["audit rows"] = string(raw)
	sinks["logs and spans"] = devSinkText(t)
	for sink, text := range sinks {
		for _, form := range secretForms(value) {
			if strings.Contains(text, form) {
				t.Errorf("the secret's value reached %s as %q", sink, form)
			}
		}
	}
}

// submitCall files a call request for calls PUT /v1/echo calls as c and
// returns its id.
func submitCall(t *testing.T, c *client, calls int) string {
	t.Helper()
	out := c.call("manage_approval", map[string]any{
		"command": "submit", "kind": "call", "tool": "api_invoke_endpoint", "calls": calls, "reason": "Acceptance #2437.",
		"arguments": map[string]any{"connection": handlerConnection, "method": "PUT", "path": "/v1/echo", "body": map[string]any{}},
	})
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("submit filed no request: %v", out)
	}
	return id
}
