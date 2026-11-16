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
	// Who a handler applies to is chosen (#2650): naming no persona and no
	// person is everyone only when it says so.
	if params["personas"] == nil && params["users"] == nil {
		params["everyone"] = true
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

// unmatchedLatency holds the handlers' own cost on a call none of them
// applies to, while 50 handlers of kind are bound to other connections
// (#2436, #2634), and measures the call end to end beside it (#2653).
//
// The handlers' layer reports its own time per call, the call itself left out
// (call_handlers_layer_seconds): at least 99% of the unmatched calls spend
// at most a millisecond in it. End to end, p50 and p99 with the handlers stay
// within 5% of without them plus an absolute allowance (2.5ms at p50, 5ms at
// p99): on a shared CI runner, two blocks of the same call differ by more
// than 5% of a few milliseconds whatever the handlers do (#2653 measured
// 0.18ms on 2.4ms and 1.9ms on 4.2ms there), so a percentage alone fails on
// the runner's noise. The two states are measured interleaved (#2645): five
// rounds of 200 calls each, the order alternating, the 50 handlers enabled or
// disabled between blocks, so both states sample the same stack.
func unmatchedLatency(t *testing.T, admin, caller *client, prefix, kind, source, entry string) {
	t.Helper()
	const rounds, block, settle = 5, 200, 20
	const toggleSettle = 3 * time.Second
	call := func() time.Duration {
		start := time.Now()
		res, text, err := caller.callRaw("api_invoke_endpoint", map[string]any{"connection": handlerConnection, "method": "GET", "path": "/v1/fixed/latency", "purpose": handlerPurpose})
		if err != nil || res.IsError {
			t.Fatalf("an unmatched call failed: %v %s", err, text)
		}
		return time.Since(start)
	}
	// The scripts are saved, and the handlers bound, before anything is
	// measured: a save starts background work of its own (the script's tile,
	// its index entry) that would otherwise land in one state's blocks.
	names := make([]string, 0, 3)
	for range 3 {
		name := handlerNonce(prefix)
		saveHandlerScript(t, admin, name, source, entry)
		names = append(names, name)
	}
	bound := make([]string, 0, 50)
	for i := range 50 {
		// A script holds at most 20 handlers of a kind.
		h := admin.handlerSet(names[i/20], kind, map[string]any{"connection": fmt.Sprintf("acc-elsewhere-%d", i), "operations": []string{"*"}, "everyone": true, "approve": i%20 == 0})
		id, _ := h["handler"].(string)
		bound = append(bound, id)
	}
	t.Cleanup(func() {
		for _, id := range bound {
			admin.handlerOff(id)
		}
	})
	toggle := func(on bool) {
		command := "handler_disable"
		if on {
			command = "handler_enable"
		}
		for _, id := range bound {
			if res, text, err := admin.callRaw("manage_script", map[string]any{"command": command, "handler": id}); err != nil || res.IsError {
				t.Fatalf("%s %s: %v %s", command, id, err, text)
			}
		}
		// Unmeasured: every toggle is heard on each replica as one change per
		// handler, and the first call after a heard change waits for the index
		// to read the handlers again. Let those notifications land and be read
		// before measuring, so neither state's block pays for the other's
		// toggle.
		time.Sleep(toggleSettle)
		for range settle {
			call()
		}
	}
	samples := map[bool][]time.Duration{}
	// The layer's time is read around each measured block alone: the settle
	// calls after a toggle pay the index's re-read of the handlers, which a
	// change of handlers costs once and is not what an unmatched call costs.
	var n, within float64
	for r := range rounds {
		// AB, BA, AB, ...: neither state always runs first in a round.
		for _, on := range []bool{r%2 == 1, r%2 == 0} {
			toggle(on)
			before := scrapeRaw(t)
			for range block {
				samples[on] = append(samples[on], call())
			}
			bn, bw := layerShare(before, scrapeRaw(t), "false", "0.001")
			n, within = n+bn, within+bw
		}
	}
	pct := func(d []time.Duration, n int) time.Duration {
		s := slices.Clone(d)
		slices.Sort(s)
		return s[len(s)*n/100]
	}
	without50, without99 := pct(samples[false], 50), pct(samples[false], 99)
	with50, with99 := pct(samples[true], 50), pct(samples[true], 99)
	t.Logf("%s: without handlers p50 %v p99 %v; with 50 elsewhere p50 %v p99 %v", kind, without50, without99, with50, with99)
	if limit := allowed(without50, latencyAllowanceP50); with50 > limit {
		t.Errorf("p50 with 50 %s handlers elsewhere is %v, over %v (5%% over %v plus %v)", kind, with50, limit, without50, latencyAllowanceP50)
	}
	if limit := allowed(without99, latencyAllowanceP99); with99 > limit {
		t.Errorf("p99 with 50 %s handlers elsewhere is %v, over %v (5%% over %v plus %v)", kind, with99, limit, without99, latencyAllowanceP99)
	}
	t.Logf("%s: the handlers' layer spent at most 1ms on %.0f of %.0f unmatched calls", kind, within, n)
	if n < rounds*block*2 || within < 0.99*n {
		t.Errorf("the handlers' layer spent at most 1ms on %.0f of %.0f unmatched calls; want at least 99%% of at least %d", within, n, rounds*block*2)
	}
}

// The absolute allowance an end-to-end latency comparison adds to 5% (#2653).
const (
	latencyAllowanceP50 = 2500 * time.Microsecond
	latencyAllowanceP99 = 5 * time.Millisecond
)

// allowed is the most a latency may be against base: 5% over it plus slack.
func allowed(base, slack time.Duration) time.Duration {
	return time.Duration(float64(base)*1.05) + slack
}

// layerShare is, of the calls the handlers' layer saw between two scrapes
// that matched (or did not) a handler, how many there were and on how many it
// spent at most le seconds (call_handlers_layer_seconds, #2653).
func layerShare(before, after, matched, le string) (n, within float64) {
	byMatch := map[string]string{"matched": matched}
	n = metricSeries(after, "call_handlers_layer_seconds_count", byMatch) - metricSeries(before, "call_handlers_layer_seconds_count", byMatch)
	byEdge := map[string]string{"matched": matched, "le": le}
	within = metricSeries(after, "call_handlers_layer_seconds_bucket", byEdge) - metricSeries(before, "call_handlers_layer_seconds_bucket", byEdge)
	return n, within
}
