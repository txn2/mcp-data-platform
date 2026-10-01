//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #1997: a webhook channel with format json posts one signed
// envelope per notification, for a system to receive.
//
// The receiver is this platform's own inbound hmac source, configured with
// event_id_path $.id and event_type_path $.type as the ticket describes, so an
// envelope lands as an event whose id and type are the envelope's. The notify
// tool's data parameter is untyped; the MCP client sends it as an object, an
// array and a string as literal tools/call params, and a script hands
// platform.notify a dict. The admin API takes one JSON object per route.

// issue1997Channel registers a webhook channel and removes it when the test
// ends.
func issue1997Channel(t *testing.T, c *client, conn, format string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1997-%d", time.Now().UnixNano())
	putChannel(t, c, name, map[string]any{"kind": "webhook", "connection": conn, "format": format})
	t.Cleanup(func() { deleteChannel(t, c, name) })
	return name
}

// issue1997Event is one envelope as the source stored it.
type issue1997Event struct {
	id, typ string
	payload map[string]any
}

// issue1997Landed waits for the source to hold an event whose title is title,
// and returns it.
func issue1997Landed(t *testing.T, c *client, source, title string) issue1997Event {
	t.Helper()
	var got issue1997Event
	waitFor(t, "the envelope titled "+title, func() (bool, string) {
		all := issue1997All(t, c, source, title)
		if len(all) == 0 {
			return false, "no event yet"
		}
		got = all[0]
		return true, ""
	})
	return got
}

// issue1997All reads every event the source holds whose title is title.
func issue1997All(t *testing.T, c *client, source, title string) []issue1997Event {
	t.Helper()
	out := c.call("trino_query", map[string]any{
		"connection": issue1870Conn, "purpose": issue1996Purpose,
		"sql": fmt.Sprintf("SELECT event_id, event_type, payload FROM %s.webhook_%s WHERE json_extract_scalar(payload, '$.title') = '%s'",
			issue1870Schema, strings.ReplaceAll(source, "-", "_"), title),
	})
	rows, _ := out["rows"].([]any)
	events := make([]issue1997Event, 0, len(rows))
	for _, r := range rows {
		row, _ := r.(map[string]any)
		var ev issue1997Event
		ev.id, _ = row["event_id"].(string)
		ev.typ, _ = row["event_type"].(string)
		raw, _ := row["payload"].(string)
		_ = json.Unmarshal([]byte(raw), &ev.payload)
		events = append(events, ev)
	}
	return events
}

// TestIssue1997_AJSONChannelDeliversToAnHMACSource is the ticket's first
// criterion: a format json channel on an hmac connection delivers to an
// inbound hmac source, and the envelope lands with event_id = id and
// event_type = type, carrying the sender's data verbatim in every form.
func TestIssue1997_AJSONChannelDeliversToAnHMACSource(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	// The channel posts to its connection's base URL, so the connection for a
	// channel names the source's whole address.
	hook := issue1996Connection(t, c, issue1996SelfURL()+"/hooks/"+source, map[string]any{"hmac_preset": "platform"})
	ch := issue1997Channel(t, c, hook, "json")

	forms := map[string]any{
		"object": map[string]any{"stores": []any{3, 7, 12}, "late": true},
		"array":  []any{"A-1001", "A-1002"},
		"string": `{"kept":"as a string"}`,
	}
	for form, data := range forms {
		title := fmt.Sprintf("acc-1997 %s %d", form, time.Now().UnixNano())
		out := c.call("notify", map[string]any{"action": "send", "channel": ch, "title": title, "body": "**12** stores", "data": data})
		if q, _ := out["queued"].(float64); q != 1 {
			t.Fatalf("%s data: the send was not queued: %v", form, out)
		}
		ev := issue1997Landed(t, c, source, title)
		if !strings.HasPrefix(ev.id, "ntf_") || ev.id != ev.payload["id"] {
			t.Errorf("%s data: event_id %q is not the envelope's id %v", form, ev.id, ev.payload["id"])
		}
		if ev.typ != "notify.sent" || ev.payload["type"] != "notify.sent" {
			t.Errorf("%s data: event_type %q, envelope type %v; want notify.sent", form, ev.typ, ev.payload["type"])
		}
		want, _ := json.Marshal(data)
		gotData, _ := json.Marshal(ev.payload["data"])
		if string(gotData) != string(want) {
			t.Errorf("%s data arrived as %s, want %s", form, gotData, want)
		}
		if ev.payload["channel"] != ch || ev.payload["body"] != "**12** stores" {
			t.Errorf("%s data: envelope = %v", form, ev.payload)
		}
	}
}

// TestIssue1997_AScriptsFindingCarriesItsData posts from a managed script with
// platform.notify(data=...): the envelope is a script.finding naming the
// script and the run, with the dict verbatim.
func TestIssue1997_AScriptsFindingCarriesItsData(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	hook := issue1996Connection(t, c, issue1996SelfURL()+"/hooks/"+source, map[string]any{"hmac_preset": "platform"})
	ch := issue1997Channel(t, c, hook, "json")

	title := fmt.Sprintf("acc-1997 finding %d", time.Now().UnixNano())
	name := fmt.Sprintf("acc-1997-monitor-%d", time.Now().UnixNano())
	script := fmt.Sprintf(`
def main():
    """Posts the stores that crossed the reorder point."""
    platform.notify(channel = %q, title = %q, body = "3 stores", data = {"stores": [3, 7, 12], "threshold": 0.2})
`, ch, title)
	created := c.saveScript(map[string]any{"command": "create", "name": name, "source": script, "display_name": "Acceptance 1997 monitor"}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := c.call("run_script", map[string]any{"name": name})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}

	// The draft the save ran posted the same title; the run's is the one
	// whose source names the run.
	var ev issue1997Event
	waitFor(t, "the run's finding", func() (bool, string) {
		seen := make([]any, 0)
		for _, e := range issue1997All(t, c, source, title) {
			src, _ := e.payload["source"].(map[string]any)
			if src["run_id"] == run["run_id"] {
				ev = e
				return true, ""
			}
			seen = append(seen, src)
		}
		return false, fmt.Sprintf("run %v; sources seen %v", run["run_id"], seen)
	})
	src, _ := ev.payload["source"].(map[string]any)
	if ev.typ != "script.finding" || src["kind"] != "script" ||
		!strings.HasPrefix(fmt.Sprint(src["script"]), "mcp:script:") {
		t.Errorf("the finding was stamped %q %v", ev.typ, src)
	}
	if id, _ := created["id"].(string); id != "" && src["script"] != "mcp:script:"+id {
		t.Errorf("source.script = %v, want mcp:script:%s", src["script"], id)
	}
	gotData, _ := json.Marshal(ev.payload["data"])
	if string(gotData) != `{"stores":[3,7,12],"threshold":0.2}` {
		t.Errorf("data arrived as %s", gotData)
	}
}

// issue1997Capture is a receiver that answers each request with the next
// status in its script, recording what it got.
type issue1997Capture struct {
	mu       sync.Mutex
	statuses []int
	got      []issue1996Captured
}

func (c *issue1997Capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, issue1996Captured{header: r.Header.Clone(), body: raw})
	status := http.StatusAccepted
	if len(c.statuses) > 0 {
		status, c.statuses = c.statuses[0], c.statuses[1:]
	}
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
}

func (c *issue1997Capture) requests() []issue1996Captured {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]issue1996Captured(nil), c.got...)
}

// TestIssue1997_ARetryCarriesTheSameID answers the first delivery 503 with
// Retry-After: the queue retries it, and the retry carries the same id, in the
// envelope and in the connection's id header. Posted to the source, the two
// deliveries are one event id, which compaction keeps once.
func TestIssue1997_ARetryCarriesTheSameID(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	recv := &issue1997Capture{statuses: []int{http.StatusServiceUnavailable}}
	srv := httptest.NewServer(recv)
	t.Cleanup(srv.Close)
	hook := issue1996Connection(t, c, srv.URL, map[string]any{"hmac_preset": "platform", "hmac_id_header": "webhook-id"})
	ch := issue1997Channel(t, c, hook, "json")

	title := fmt.Sprintf("acc-1997 retry %d", time.Now().UnixNano())
	c.call("notify", map[string]any{"action": "send", "channel": ch, "title": title, "data": map[string]any{"n": 1}})
	waitFor(t, "the retried delivery", func() (bool, string) {
		n := len(recv.requests())
		return n >= 2, fmt.Sprint(n, " deliveries")
	})
	reqs := recv.requests()
	ids := make([]string, 0, 2)
	for _, r := range reqs[:2] {
		var env map[string]any
		if err := json.Unmarshal(r.body, &env); err != nil {
			t.Fatalf("a delivery is not JSON: %s", r.body)
		}
		if r.header.Get("webhook-id") != env["id"] {
			t.Errorf("the id header %q is not the envelope's id %v", r.header.Get("webhook-id"), env["id"])
		}
		ids = append(ids, fmt.Sprint(env["id"]))
	}
	if ids[0] != ids[1] || !strings.HasPrefix(ids[0], "ntf_") {
		t.Fatalf("the retry carried id %s after %s; a retry carries the same id", ids[1], ids[0])
	}
	for i, r := range reqs[:2] {
		res, body := issue1870Post(t, baseURL(), source, "application/json", r.body, map[string]string{
			"X-Signature": r.header.Get("X-Signature"), "X-Timestamp": r.header.Get("X-Timestamp"),
		})
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("delivery %d posted to the source: %d %s", i+1, res.StatusCode, body)
		}
	}
	distinct := issue1870Count(t, c, fmt.Sprintf("SELECT count(DISTINCT event_id) FROM %s.webhook_%s",
		issue1870Schema, strings.ReplaceAll(source, "-", "_")))
	if distinct != 1 {
		t.Errorf("the two deliveries are %d event ids at the source, want 1", distinct)
	}
}

// TestIssue1997_ATextChannelIgnoresData sends data to a channel with no
// format: it delivers {"text": ...} as it did before formats existed.
func TestIssue1997_ATextChannelIgnoresData(t *testing.T) {
	c := connect(t)
	recv := &issue1997Capture{}
	srv := httptest.NewServer(recv)
	t.Cleanup(srv.Close)
	hook := issue1996Connection(t, c, srv.URL, map[string]any{"auth_mode": "none", "credential": ""})
	name := fmt.Sprintf("acc-1997-text-%d", time.Now().UnixNano())
	putChannel(t, c, name, map[string]any{"kind": "webhook", "connection": hook})
	t.Cleanup(func() { deleteChannel(t, c, name) })

	title := fmt.Sprintf("acc-1997 text %d", time.Now().UnixNano())
	c.call("notify", map[string]any{"action": "send", "channel": name, "title": title, "data": map[string]any{"n": 1}})
	waitFor(t, "the text delivery", func() (bool, string) { return len(recv.requests()) >= 1, "" })
	var body map[string]any
	if err := json.Unmarshal(recv.requests()[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || !strings.Contains(fmt.Sprint(body["text"]), title) {
		t.Errorf("a text channel posted %v, want only {\"text\": ...}", body)
	}
}

// TestIssue1997_SendTestDeliversASignedTestEnvelope presses Send test on a
// json channel: the receiver verifies the signature and stores a test event.
func TestIssue1997_SendTestDeliversASignedTestEnvelope(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	hook := issue1996Connection(t, c, issue1996SelfURL()+"/hooks/"+source, map[string]any{"hmac_preset": "platform"})
	ch := issue1997Channel(t, c, hook, "json")

	status, out := testChannel(t, c, ch)
	if status != http.StatusOK || out["delivered"] != true {
		t.Fatalf("Send test: %d %v", status, out)
	}
	ev := issue1997Landed(t, c, source, "Test message from the data platform")
	if ev.typ != "test" || !strings.HasPrefix(ev.id, "ntf_test_") {
		t.Errorf("the test landed as %q %q", ev.typ, ev.id)
	}
}
