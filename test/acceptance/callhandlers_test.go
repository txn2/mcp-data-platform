//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What every criterion about a tool-call handler (#2436, #2634, #2635, #2437)
// shares: a policy, transform or watch script saved and bound to a connection
// by an administrator, the api-test fixture as the upstream, and reading back
// what the fixture received.

const (
	// handlerConnection is the api connection the dev stack registers for
	// the api-test fixture.
	handlerConnection = "api-test-fixture"
	// handlerFixtureURL is where the fixture answers, and records every
	// request it receives with its headers and body.
	handlerFixtureURL = "http://localhost:9282"
	// handlerTraceHeader is the header a criterion marks its own calls
	// with, so it can find them among the fixture's records while other
	// criteria use the same fixture.
	handlerTraceHeader = "X-Trace-Id"
	// handlerFixtureKey is the key the dev stack gives the fixture, which
	// its audit API reads.
	handlerFixtureKey = "apitest-dev-key-2024"
	// handlerPurpose is the sentence the purpose gate requires of a data
	// call.
	handlerPurpose = "The acceptance suite is exercising a tool-call handler criterion."
)

// handlerNonce is a name no earlier run used.
func handlerNonce(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano()%1e10, 36)
}

// sampleCall is the call a draft of a handler script is drafted with: an
// echo write of two items, as decide(call) and transform(call, result) are
// given it.
const sampleCall = `{"tool": "api_invoke_endpoint", "connection": "api-test-fixture", "operation": "PUT /v1/echo", ` +
	`"method": "PUT", "path": "/v1/echo", "path_params": {}, "query": {}, "arguments": {}, ` +
	`"body": {"subscribers": [{"email": "a@example.com"}, {"email": "b@example.com"}]}, "items": 2, "write": True, ` +
	`"user": "asset.owner@example.com", "persona": "collaborator", "time": "2026-11-13T15:00:00Z"}`

// drafted is source with a main() that runs entry on each sample call
// (sampleCall when none is given), so the save's recorded test replays what
// entry calls and reaches each branch the samples take.
func drafted(source, entry string, samples ...string) string {
	if len(samples) == 0 {
		samples = []string{sampleCall}
	}
	var b strings.Builder
	b.WriteString(source + "\ndef main():\n    \"\"\"Drafts the sample calls.\"\"\"\n")
	for _, c := range samples {
		args := c
		if entry == "transform" {
			args = c + `, {"status": 200, "body": {"card": {"number": "4111 1111 1111 1111"}}, "rows": [{"ssn": "123-45-6789"}]}`
		}
		b.WriteString("    print(" + entry + "(" + args + "))\n")
	}
	return b.String()
}

// sampleWith is the sample call with body in place of its own.
func sampleWith(body string) string {
	return strings.Replace(sampleCall, `"body": {"subscribers": [{"email": "a@example.com"}, {"email": "b@example.com"}]}`, `"body": `+body, 1)
}

// saveHandlerScript saves source as a script named name, drafted through
// entry (decide, transform, or main for a watch) on samples, as c.
func saveHandlerScript(t *testing.T, c *client, name, source, entry string, samples ...string) {
	t.Helper()
	if entry != "main" {
		source = drafted(source, entry, samples...)
	}
	c.saveScript(map[string]any{"command": "create", "name": name, "source": source}, nil)
}

// bindHandler binds the script name as an administrator, approving its latest
// version for handlers as that administrator, and returns the handler.
func bindHandler(t *testing.T, admin *client, name, owner, kind string, settings map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"connection": handlerConnection, "approve": true}
	if owner != "" {
		params["owner_email"] = owner
	}
	for k, v := range settings {
		params[k] = v
	}
	h := admin.handlerSet(name, kind, params)
	t.Cleanup(func() { admin.handlerOff(h["handler"]) })
	return h
}

// handlerError is a refused call's error envelope: code, category, message
// and hint, or nil for a call that ran.
func handlerError(res *mcp.CallToolResult) map[string]any {
	if res == nil || !res.IsError {
		return nil
	}
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if e, ok := sc["error"].(map[string]any); ok {
			return e
		}
	}
	text := firstText(res)
	var env map[string]any
	if json.Unmarshal([]byte(text), &env) == nil {
		if e, ok := env["error"].(map[string]any); ok {
			return e
		}
	}
	return map[string]any{"message": text}
}

// codeOf is a refused call's error code, "" for a call that ran.
func codeOf(res *mcp.CallToolResult) string {
	e := handlerError(res)
	if e == nil {
		return ""
	}
	code, _ := e["code"].(string)
	// The error contract writes codes in snake case (approval_required); the
	// criteria name them as the refusal's text opens, upper case.
	return strings.ToUpper(code)
}

// echoPut is api_invoke_endpoint PUT /v1/echo on the fixture as c, carrying
// body (an object, or a string of JSON: the two forms the schema admits) and
// the trace header, so the fixture's record of it can be found.
func echoPut(t *testing.T, c *client, trace string, body any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": handlerConnection, "method": "PUT", "path": "/v1/echo",
		"headers": map[string]any{handlerTraceHeader: trace}, "body": body, "purpose": handlerPurpose,
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint PUT /v1/echo: transport error: %v", err)
	}
	return res, text
}

// fixtureReceived counts the requests the fixture recorded since since that
// carry the trace header with trace.
func fixtureReceived(t *testing.T, since time.Time, trace string) int {
	t.Helper()
	return len(fixtureBodies(t, since, trace))
}

// fixtureBodies is the body of every request the fixture recorded since since
// that carries the trace header with trace, oldest first.
func fixtureBodies(t *testing.T, since time.Time, trace string) []string {
	t.Helper()
	q := url.Values{"from": {since.Add(-time.Second).UTC().Format(time.RFC3339)}, "limit": {"200"}}
	var list map[string]any
	fixtureGet(t, "/api/v1/portal/audit/events?"+q.Encode(), &list)
	var bodies []string
	for _, ev := range rowsOf(list) {
		id, _ := ev["id"].(string)
		if id == "" {
			continue
		}
		var detail map[string]any
		fixtureGet(t, "/api/v1/portal/audit/events/"+url.PathEscape(id), &detail)
		raw, _ := json.Marshal(detail)
		if strings.Contains(string(raw), trace) {
			bodies = append(bodies, fmt.Sprint(detail["request_body"]))
		}
	}
	return bodies
}

// rowsOf is the event list in a fixture answer, however it is keyed.
func rowsOf(list map[string]any) []map[string]any {
	for _, key := range []string{"events", "data", "items"} {
		if rows, ok := list[key].([]any); ok {
			out := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				if m, ok := r.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out
		}
	}
	return nil
}

func fixtureGet(t *testing.T, path string, into any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, handlerFixtureURL+path, http.NoBody) //nolint:noctx // a loopback fixture
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", handlerFixtureKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the api-test fixture does not answer at %s (%v); start the dev stack with `make dev`", handlerFixtureURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s on the fixture: %d %s", path, res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("GET %s on the fixture: %v\n%s", path, err, raw)
	}
}

// subscribers is a body of n subscribers, as an object.
func subscribers(n int) map[string]any {
	list := make([]any, 0, n)
	for i := range n {
		list = append(list, map[string]any{"email": fmt.Sprintf("s%d@example.com", i)})
	}
	return map[string]any{"subscribers": list}
}

// asJSONString is body as the other form the schema admits: a string of JSON.
func asJSONString(t *testing.T, body any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// bodyForms is body in both forms api_invoke_endpoint's body admits, named.
func bodyForms(t *testing.T, body any) map[string]any {
	t.Helper()
	return map[string]any{"object": body, "string": asJSONString(t, body)}
}

// tempPersona creates a persona of its own for one criterion, with every tool
// and connection and the API route rules routes (nil for none), and a service
// key whose role resolves to it. It returns the persona's name, its caller's
// client, and the persona request body a criterion edits to change the
// persona. Its own persona and caller keep a criterion that changes a grant,
// or holds an allowance, from reaching any other criterion.
func tempPersona(t *testing.T, admin *client, prefix string, routes []any) (string, *client, map[string]any) {
	t.Helper()
	persona := handlerNonce(prefix)
	role := strings.ReplaceAll(persona, "-", "_")
	body := map[string]any{
		"name": persona, "display_name": "Acceptance " + prefix, "roles": []string{role},
		"allow_tools": []string{"*"}, "allow_connections": []string{"*"},
	}
	if routes != nil {
		body["api_routes"] = routes
	}
	if code := admin.restJSON(http.MethodPost, "/api/v1/admin/personas", body); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("creating persona %s: HTTP %d", persona, code)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, nil) })
	status, key := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{"name": persona, "roles": []string{role}, "expires_in": "1h"}))
	if status != http.StatusCreated {
		t.Fatalf("minting a key for %s: %d %v", persona, status, key)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+persona, nil) })
	return persona, connectAs(t, fmt.Sprint(key["key"])), body
}

// handlerTest is manage_script handler_test as c: what every handler on the
// connection would make of the call, without sending it.
func handlerTest(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"command": "handler_test", "connection": handlerConnection}
	for k, v := range args {
		params[k] = v
	}
	return c.call("manage_script", params)
}

// unmatchedLatency measures 1,000 unmatched tool calls as caller with no
// handler bound, with 50 handlers of kind (on scripts of source, drafted
// through entry) bound to other connections, and with none again, and fails when the p50 or p99 with the 50
// is more than 5% over the mean of the two runs without: the runs before and
// after are averaged so drift in the stack over the run is not read as the
// handlers' cost.
func unmatchedLatency(t *testing.T, admin, caller *client, prefix, kind, source, entry string) {
	t.Helper()
	measure := func() (p50, p99 time.Duration) {
		d := make([]time.Duration, 0, 1000)
		for range 1000 {
			start := time.Now()
			res, text, err := caller.callRaw("api_invoke_endpoint", map[string]any{"connection": handlerConnection, "method": "GET", "path": "/v1/fixed/latency", "purpose": handlerPurpose})
			if err != nil || res.IsError {
				t.Fatalf("an unmatched call failed: %v %s", err, text)
			}
			d = append(d, time.Since(start))
		}
		slices.Sort(d)
		return d[len(d)/2], d[len(d)*99/100]
	}
	// The scripts are saved, and a warm-up run, before anything is measured:
	// a save starts background work of its own (the script's tile, its
	// index entry) that would otherwise land in one phase and not the others.
	names := make([]string, 0, 3)
	for range 3 {
		name := handlerNonce(prefix)
		saveHandlerScript(t, admin, name, source, entry)
		names = append(names, name)
	}
	measure()
	before50, before99 := measure()
	var bound []any
	for i := range 50 {
		// A script holds at most 20 handlers of a kind.
		h := admin.handlerSet(names[i/20], kind, map[string]any{"connection": fmt.Sprintf("acc-elsewhere-%d", i), "operations": []string{"*"}, "approve": i%20 == 0})
		bound = append(bound, h["handler"])
	}
	with50, with99 := measure()
	for _, h := range bound {
		admin.handlerOff(h)
	}
	after50, after99 := measure()
	base50, base99 := (before50+after50)/2, (before99+after99)/2
	if float64(with50) > float64(base50)*1.05 {
		t.Errorf("p50 with 50 %s handlers elsewhere is %v, more than 5%% over %v", kind, with50, base50)
	}
	if float64(with99) > float64(base99)*1.05 {
		t.Errorf("p99 with 50 %s handlers elsewhere is %v, more than 5%% over %v", kind, with99, base99)
	}
}
