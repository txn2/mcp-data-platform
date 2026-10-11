//go:build integration

package acceptance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2009: the MCP Tasks extension. A client that declares
// io.modelcontextprotocol/tasks on its tools/call is answered by run_script and
// the export tools with a task, and follows it with tasks/get and tasks/cancel.
//
// The SDK client the rest of the suite drives cannot speak the extension, so
// these criteria are made by a raw 2026-07-28 client over HTTP, exactly as one
// that implements the extension sends them: no initialize, the protocol
// version, client info and capabilities in each request's _meta, and the
// Mcp-Method / Mcp-Name headers. The session handle and the discovery the
// search-first gate requires come from the suite's ordinary session.
//
// Wire forms: tools/call params are an object with name (string) and
// arguments (object); run_script's args is a typed object and wait_seconds a
// typed integer; tasks/get and tasks/cancel take taskId, a string; tasks/update
// takes taskId and inputResponses, an object. Each admits one JSON form, and
// each is sent below as a literal parameter of that form. The extension is
// declared as an empty object under clientCapabilities.extensions, its only
// form.

const (
	issue2009Protocol  = "2026-07-28"
	issue2009Extension = "io.modelcontextprotocol/tasks"
	issue2009Purpose   = "Acceptance for #2009: run_script and the exports as MCP tasks."
	issue2009Slow      = "mcp-test-fixture__slow"
)

// taskWire is a raw 2026-07-28 client for one identity.
type taskWire struct {
	t       *testing.T
	base    string
	key     string
	session string
	id      int64
}

// wireFor is a raw client sharing c's base, credential and session handle.
func wireFor(c *client) *taskWire {
	return &taskWire{t: c.t, base: c.base, key: c.apiKey, session: c.sessionID}
}

// rpcAnswer is one JSON-RPC response: its result, or its error.
type rpcAnswer struct {
	Result map[string]any `json:"result"`
	Error  *struct {
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// send issues one request. declare puts the extension in the request's own
// client capabilities; name is the Mcp-Name header (a tool name or a task id).
func (w *taskWire) send(method, name string, params map[string]any, declare bool) rpcAnswer {
	w.t.Helper()
	w.id++
	caps := map[string]any{}
	if declare {
		caps["extensions"] = map[string]any{issue2009Extension: map[string]any{}}
	}
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    issue2009Protocol,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "acceptance-2009", "version": "1.0.0"},
		"io.modelcontextprotocol/clientCapabilities": caps,
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": w.id, "method": method, "params": params})
	if err != nil {
		w.t.Fatalf("%s: encoding the request: %v", method, err)
	}
	req, err := http.NewRequestWithContext(w.t.Context(), http.MethodPost, w.base, bytes.NewReader(body)) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		w.t.Fatalf("%s: %v", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+w.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", issue2009Protocol)
	req.Header.Set("Mcp-Method", method)
	if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		w.t.Fatalf("%s: %v", method, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	return w.read(method, res)
}

// read finds this request's response in a JSON body or an event stream.
func (w *taskWire) read(method string, res *http.Response) rpcAnswer {
	w.t.Helper()
	var payloads [][]byte
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(res.Body)
		scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data:"); ok {
				payloads = append(payloads, []byte(strings.TrimSpace(data)))
			}
		}
	} else {
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(res.Body); err != nil {
			w.t.Fatalf("%s: reading the response: %v", method, err)
		}
		payloads = append(payloads, buf.Bytes())
	}
	for _, p := range payloads {
		var msg struct {
			ID json.Number `json:"id"`
			rpcAnswer
		}
		if json.Unmarshal(p, &msg) == nil && msg.ID.String() == fmt.Sprint(w.id) {
			return msg.rpcAnswer
		}
	}
	w.t.Fatalf("%s: HTTP %d carried no response to request %d", method, res.StatusCode, w.id)
	return rpcAnswer{}
}

// callTool sends tools/call with the session handle attached.
func (w *taskWire) callTool(tool string, args map[string]any, declare bool) rpcAnswer {
	w.t.Helper()
	args["session_id"] = w.session
	return w.send("tools/call", tool, map[string]any{"name": tool, "arguments": args}, declare)
}

// task sends tasks/get, tasks/cancel or tasks/update for id.
func (w *taskWire) task(method, id string, extra map[string]any, declare bool) rpcAnswer {
	w.t.Helper()
	params := map[string]any{"taskId": id}
	for k, v := range extra {
		params[k] = v
	}
	return w.send(method, id, params, declare)
}

// handle reads the task a tools/call was answered with.
func (w *taskWire) handle(a rpcAnswer, prefix string) string {
	w.t.Helper()
	if a.Error != nil {
		w.t.Fatalf("tools/call refused: %d %s", a.Error.Code, a.Error.Message)
	}
	if a.Result["resultType"] != "task" {
		w.t.Fatalf("the call was answered directly, not with a task: %v", a.Result)
	}
	id, _ := a.Result["taskId"].(string)
	if !strings.HasPrefix(id, prefix) {
		w.t.Fatalf("taskId = %q; want one starting %q: %v", id, prefix, a.Result)
	}
	if a.Result["status"] != "working" {
		w.t.Errorf("a new task's status = %v; want working", a.Result["status"])
	}
	if _, ok := a.Result["ttlMs"]; !ok {
		w.t.Errorf("the task carries no ttlMs: %v", a.Result)
	}
	if poll, _ := a.Result["pollIntervalMs"].(float64); poll <= 0 {
		w.t.Errorf("pollIntervalMs = %v; want the interval to poll at", a.Result["pollIntervalMs"])
	}
	return id
}

// await polls tasks/get until the task reaches a terminal status, calling
// seen with every working answer, and returns the terminal one.
func (w *taskWire) await(id string, within time.Duration, seen func(map[string]any)) map[string]any {
	w.t.Helper()
	deadline := time.Now().Add(within)
	for {
		a := w.task("tasks/get", id, nil, true)
		if a.Error != nil {
			w.t.Fatalf("tasks/get %s: %d %s", id, a.Error.Code, a.Error.Message)
		}
		if a.Result["resultType"] != "complete" {
			w.t.Errorf("tasks/get resultType = %v; want complete", a.Result["resultType"])
		}
		status, _ := a.Result["status"].(string)
		if status != "working" {
			return a.Result
		}
		if seen != nil {
			seen(a.Result)
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("task %s still working after %s: %v", id, within, a.Result)
		}
		poll, _ := a.Result["pollIntervalMs"].(float64)
		time.Sleep(time.Duration(poll) * time.Millisecond)
	}
}

// toolText reads the JSON a tool result's first text block carries.
func toolText(t *testing.T, result any) map[string]any {
	t.Helper()
	res, _ := result.(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("the result carries no content: %v", result)
	}
	block, _ := content[0].(map[string]any)
	var out map[string]any
	if err := json.Unmarshal([]byte(fmt.Sprint(block["text"])), &out); err != nil {
		t.Fatalf("the result's text is not JSON: %v (%v)", err, block["text"])
	}
	return out
}

// issue2009Script saves a script that reports progress before each of holds
// slow calls and hands back how many it made.
func issue2009Script(t *testing.T, c *client) string {
	t.Helper()
	name := fmt.Sprintf("acc-2009-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2009: a run followed as an MCP task.",
		"params": []any{
			map[string]any{"name": "hold_ms", "type": "int", "required": true, "description": "How long each slow call lasts."},
			map[string]any{"name": "holds", "type": "int", "required": true, "description": "How many slow calls the run makes."},
		},
		"source": `def main():
    """Reports progress before each slow call, then hands back how many it made."""
    for i in range(run.params["holds"]):
        platform.progress("holding the run open", done = i, total = run.params["holds"])
        platform.call("` + issue2009Slow + `", {"milliseconds": run.params["hold_ms"]})
    platform.result({"holds": run.params["holds"]})
`,
	}, map[string]any{"hold_ms": 10, "holds": 1})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// TestIssue2009_TheServerAdvertisesTheExtension: a client learns the
// extension is offered from the server's capabilities.
func TestIssue2009_TheServerAdvertisesTheExtension(t *testing.T) {
	c := connect(t)
	caps := c.session.InitializeResult().Capabilities
	if caps == nil || caps.Extensions == nil {
		t.Fatalf("the server declares no extensions: %+v", caps)
	}
	if _, ok := caps.Extensions[issue2009Extension]; !ok {
		t.Fatalf("the server does not declare %s: %v", issue2009Extension, caps.Extensions)
	}
}

// TestIssue2009_ARunScriptTaskReportsProgressAndItsResult: run_script started
// as a task is handed back at once, its progress is the task's status, and
// tasks/get returns the run's result once it ends. The task is the run.
func TestIssue2009_ARunScriptTaskReportsProgressAndItsResult(t *testing.T) {
	c := connect(t)
	name := issue2009Script(t, c)
	w := wireFor(c)

	started := time.Now()
	id := w.handle(w.callTool("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 3000, "holds": 3},
	}, true), "dpx_")
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("the task was handed back after %s; want at once, not after the run", took)
	}

	var progress string
	done := w.await(id, 90*time.Second, func(working map[string]any) {
		if msg, _ := working["statusMessage"].(string); msg == "holding the run open" {
			progress = msg
		}
	})
	if progress == "" {
		t.Errorf("no tasks/get reported the run's platform.progress as its statusMessage")
	}
	if done["status"] != "completed" {
		t.Fatalf("the task ended %v; want completed: %v", done["status"], done)
	}
	answer := toolText(t, done["result"])
	if answer["status"] != "succeeded" || answer["run_id"] != id {
		t.Errorf("the task's result is not the run's: %v", answer)
	}
	if got, _ := answer["result"].(map[string]any); got["holds"] != float64(3) {
		t.Errorf("result = %v; want the platform.result the run set", answer["result"])
	}

	// The task id is the run id: get_run reads the same run.
	run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if run["status"] != "succeeded" {
		t.Errorf("get_run %s = %v; want the run the task reported", id, run["status"])
	}
}

// TestIssue2009_CancellingTheTaskCancelsTheRun: tasks/cancel is
// acknowledged at once, and the run ends canceled.
func TestIssue2009_CancellingTheTaskCancelsTheRun(t *testing.T) {
	c := connect(t)
	name := issue2009Script(t, c)
	w := wireFor(c)
	id := w.handle(w.callTool("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 8000, "holds": 12},
	}, true), "dpx_")
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": id}) })
	await1986(t, c, id, "running")

	ack := w.task("tasks/cancel", id, nil, true)
	delete(ack.Result, "_meta") // the server's info, which a 2026-07-28 response carries on every result
	if ack.Error != nil || ack.Result["resultType"] != "complete" || len(ack.Result) != 1 {
		t.Fatalf("tasks/cancel = %+v; want the empty acknowledgement", ack)
	}
	done := w.await(id, 60*time.Second, nil)
	if done["status"] != "cancelled" {
		t.Fatalf("the task ended %v; want cancelled: %v", done["status"], done)
	}
	if _, ok := done["result"]; ok {
		t.Errorf("a cancelled task carries a result: %v", done)
	}
	run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if run["status"] != "canceled" {
		t.Errorf("get_run %s = %v; want canceled", id, run["status"])
	}
}

// TestIssue2009_AClientWithoutTheExtensionGetsTodaysAnswer: the same
// run_script call from a client that does not declare the extension waits for
// the run and answers with it, and tasks/* is refused to such a client.
func TestIssue2009_AClientWithoutTheExtensionGetsTodaysAnswer(t *testing.T) {
	c := connect(t)
	name := issue2009Script(t, c)
	w := wireFor(c)

	a := w.callTool("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 10, "holds": 1}, "wait_seconds": 60,
	}, false)
	if a.Error != nil {
		t.Fatalf("run_script: %d %s", a.Error.Code, a.Error.Message)
	}
	if a.Result["resultType"] == "task" {
		t.Fatalf("a client that did not declare the extension was handed a task: %v", a.Result)
	}
	answer := toolText(t, a.Result)
	if answer["status"] != "succeeded" {
		t.Errorf("run_script answered %v; want the finished run", answer)
	}

	// The SDK client the rest of the suite uses gets the same.
	sync := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"hold_ms": 10, "holds": 1}, "wait_seconds": 60})
	if sync["status"] != "succeeded" {
		t.Errorf("run_script through the SDK client = %v; want the finished run", sync)
	}

	runID, _ := answer["run_id"].(string)
	refused := w.task("tasks/get", runID, nil, false)
	if refused.Error == nil || refused.Error.Code != -32021 {
		t.Fatalf("tasks/get from a client without the extension = %+v; want -32021", refused)
	}
	if !strings.Contains(string(refused.Error.Data), issue2009Extension) {
		t.Errorf("the refusal does not name the capability: %s", refused.Error.Data)
	}
}

// TestIssue2009_ATaskIsFollowedOnlyByWhoeverMayReadIt: another person, and an
// id nothing holds, are told the task does not exist; tasks/update is
// acknowledged for the caller's own task.
func TestIssue2009_ATaskIsFollowedOnlyByWhoeverMayReadIt(t *testing.T) {
	c := connect(t)
	name := issue2009Script(t, c)
	w := wireFor(c)
	id := w.handle(w.callTool("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 10, "holds": 1},
	}, true), "dpx_")

	peer := wireFor(connectAs(t, devPeerAPIKey))
	for _, method := range []string{"tasks/get", "tasks/cancel"} {
		if a := peer.task(method, id, nil, true); a.Error == nil || a.Error.Code != -32602 {
			t.Errorf("%s by another person = %+v; want -32602", method, a)
		}
	}
	if a := w.task("tasks/get", "dpx_00000000000000000000000000000000", nil, true); a.Error == nil || a.Error.Code != -32602 {
		t.Errorf("tasks/get of an id nothing holds = %+v; want -32602", a)
	}
	ack := w.task("tasks/update", id, map[string]any{"inputResponses": map[string]any{}}, true)
	if ack.Error != nil || ack.Result["resultType"] != "complete" {
		t.Errorf("tasks/update = %+v; want the acknowledgement", ack)
	}
	w.await(id, 60*time.Second, nil)
}

// exportAsTask runs one export tool as a task and returns the tool result the
// task completed with.
func exportAsTask(t *testing.T, w *taskWire, tool string, args map[string]any) map[string]any {
	t.Helper()
	id := w.handle(w.callTool(tool, args, true), "dpt_")
	done := w.await(id, 90*time.Second, nil)
	if done["status"] != "completed" {
		t.Fatalf("%s task ended %v: %v", tool, done["status"], done)
	}
	result, _ := done["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("%s task completed with an error: %v", tool, result)
	}
	return toolText(t, result)
}

// TestIssue2009_ATrinoExportIsATask: trino_export from a declaring client is
// a task, followed through tasks/get (on whichever replica answers), whose
// result is the export's; without the extension it is answered directly.
func TestIssue2009_ATrinoExportIsATask(t *testing.T) {
	c := connect(t)
	w := wireFor(c)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	args := func() map[string]any {
		return map[string]any{
			"sql": "SELECT 1 AS store_id, 10 AS units", "format": "csv",
			"name": "Acceptance 2009 trino " + stamp, "purpose": issue2009Purpose,
		}
	}
	out := exportAsTask(t, w, "trino_export", args())
	if id, _ := out["asset_id"].(string); id == "" {
		t.Fatalf("the export task's result names no asset: %v", out)
	}

	direct := w.callTool("trino_export", args(), false)
	if direct.Error != nil || direct.Result["resultType"] == "task" {
		t.Fatalf("trino_export without the extension = %+v; want its result", direct)
	}
	if id, _ := toolText(t, direct.Result)["asset_id"].(string); id == "" {
		t.Errorf("trino_export without the extension wrote no asset: %v", direct.Result)
	}
}

// TestIssue2009_AnAPIExportIsATask: the same for api_export, at the api-test
// fixture.
func TestIssue2009_AnAPIExportIsATask(t *testing.T) {
	c := connect(t)
	out := exportAsTask(t, wireFor(c), "api_export", map[string]any{
		"connection": issue1587FixtureConn, "method": "GET", "path": "/v1/whoami",
		"name": fmt.Sprintf("Acceptance 2009 api %d", time.Now().UnixNano()), "purpose": issue2009Purpose,
	})
	if id, _ := out["asset_id"].(string); id == "" {
		t.Fatalf("the export task's result names no asset: %v", out)
	}
}

// TestIssue2009_AGraphQLExportIsATask: the same for graphql_export.
func TestIssue2009_AGraphQLExportIsATask(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	connection := issue1277Connect(t, c, "export-2009", nil)
	out := exportAsTask(t, wireFor(c), "graphql_export", map[string]any{
		"connection": connection, "query": "{ __typename }",
		"name": fmt.Sprintf("Acceptance 2009 graphql %d", time.Now().UnixNano()), "purpose": issue2009Purpose,
	})
	if id, _ := out["asset_id"].(string); id == "" {
		t.Fatalf("the export task's result names no asset: %v", out)
	}
}
