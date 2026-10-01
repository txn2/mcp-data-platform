//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #1996: an outbound auth_mode hmac signs every request a
// connection sends, so a script can deliver to a webhook receiver.
//
// The receiver is this platform's own inbound source (/hooks/{source}, #1870),
// configured as the webhook documentation configures one, and the connection
// reaches it at the replica's own address. api_invoke_endpoint's body admits an
// object and a string of JSON; the script sends an object through
// platform.call, and the MCP client sends both forms as literal tools/call
// params. The admin API takes one JSON object per route.

const (
	issue1996Purpose = "Acceptance for #1996: deliver to a webhook receiver through an hmac connection."
	issue1996Secret  = "whsec_acceptance_1996_do_not_echo"
)

// issue1996SelfURL is the address a platform replica reaches itself at. The
// dev stack runs both replicas on the host, so this is a host port.
func issue1996SelfURL() string {
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	return "http://127.0.0.1:" + port
}

// issue1996Source creates an inbound hmac source configured as the webhook
// documentation configures one, with the given tolerance.
func issue1996Source(t *testing.T, c *client, tolerance int) string {
	t.Helper()
	name := issue1870Name("hmac1996")
	issue1870Create(t, c, map[string]any{
		"name": name, "connection": issue1870Conn,
		"auth": map[string]any{
			"mode": "hmac", "secret": issue1996Secret, "signature_header": "X-Signature", "prefix": "sha256=",
			"timestamp_header": "X-Timestamp", "signed": "timestamp.body", "tolerance_seconds": tolerance,
		},
		"config": map[string]any{"event_id_path": "$.id", "event_type_path": "$.type"},
	})
	return name
}

// issue1996Connection registers an api connection and removes it when the
// test ends.
func issue1996Connection(t *testing.T, c *client, base string, extra map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1996-%d", time.Now().UnixNano())
	cfg := map[string]any{
		"base_url": base, "connection_name": name, "auth_mode": "hmac", "credential": issue1996Secret,
		"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": cfg, "description": "Acceptance 1996: an hmac connection.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection %s: HTTP %d", name, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	return name
}

// issue1996Events counts the source's events with the given id.
func issue1996Events(t *testing.T, c *client, source, id string) int {
	t.Helper()
	return issue1870Count(t, c, fmt.Sprintf("SELECT count(*) FROM %s.webhook_%s WHERE event_id = '%s'",
		issue1870Schema, strings.ReplaceAll(source, "-", "_"), id))
}

// TestIssue1996_AScriptDeliversToAnHMACSource is the ticket's integration
// criterion: a script on the local stack POSTs through an hmac connection to
// an inbound hmac source on the same platform, is answered 202, and the event
// lands in webhook_{source}.
func TestIssue1996_AScriptDeliversToAnHMACSource(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	conn := issue1996Connection(t, c, issue1996SelfURL(), map[string]any{"hmac_preset": "platform"})

	name := fmt.Sprintf("acc-1996-deliver-%d", time.Now().UnixNano())
	script := fmt.Sprintf(`
def main():
    """Delivers one order event to a partner's webhook receiver."""
    out = platform.call("api_invoke_endpoint", {
        "connection": %q,
        "method": "POST",
        "path": "/hooks/%s",
        "body": {"id": run.params["marker"], "type": "order.created", "order": "A-1001"},
        "purpose": "Deliver an order event to the partner's webhook receiver.",
    })
    if out["status"] != 202:
        fail("the receiver answered %%d" %% out["status"])
    platform.result({"status": out["status"]})
`, conn, source)
	c.saveScript(map[string]any{
		"command": "create", "name": name, "source": script,
		"display_name": "Acceptance 1996 delivery",
		"params":       []any{map[string]any{"name": "marker", "type": "string", "required": true}},
	}, map[string]any{"marker": "draft-" + source})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	marker := "run-" + source
	run := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"marker": marker}})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed, so the receiver did not answer 202: %v", run)
	}
	if n := issue1996Events(t, c, source, marker); n != 1 {
		t.Fatalf("webhook_%s holds %d events with id %s, want 1", source, n, marker)
	}
	if strings.Contains(fmt.Sprint(run), issue1996Secret) {
		t.Error("the run's record quotes the signing secret")
	}
}

// TestIssue1996_TheToolSendsBothBodyForms sends the body as an object and as
// a string of JSON, the two forms api_invoke_endpoint's body admits, through
// the MCP client; both are signed over the bytes sent and both land.
func TestIssue1996_TheToolSendsBothBodyForms(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	conn := issue1996Connection(t, c, issue1996SelfURL(), map[string]any{"hmac_preset": "platform"})

	forms := map[string]any{
		"object": map[string]any{"id": "object-" + source, "type": "order.created"},
		"string": `{"id": "string-` + source + `", "type": "order.created"}`,
	}
	for form, body := range forms {
		out := c.call("api_invoke_endpoint", map[string]any{
			"connection": conn, "method": "POST", "path": "/hooks/" + source, "body": body, "purpose": issue1996Purpose,
		})
		if status, _ := out["status"].(float64); status != http.StatusAccepted {
			t.Fatalf("%s body: the receiver answered %v: %v", form, out["status"], out)
		}
		if n := issue1996Events(t, c, source, form+"-"+source); n != 1 {
			t.Errorf("%s body: %d events landed, want 1", form, n)
		}
	}
}

// issue1996Captured is one request a capturing receiver got.
type issue1996Captured struct {
	header http.Header
	body   []byte
}

// TestIssue1996_AReplayAfterTheToleranceIsRefused captures a request the
// connection signed, replays it to the source within its tolerance (202, so
// the capture is the request as signed), then after the tolerance (401).
func TestIssue1996_AReplayAfterTheToleranceIsRefused(t *testing.T) {
	c := connect(t)
	const tolerance = 2
	source := issue1996Source(t, c, tolerance)

	var mu sync.Mutex
	var got []issue1996Captured
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, issue1996Captured{header: r.Header.Clone(), body: raw})
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(capture.Close)
	conn := issue1996Connection(t, c, capture.URL, map[string]any{"hmac_preset": "platform"})

	c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "POST", "path": "/hooks/" + source,
		"body": map[string]any{"id": "replay-" + source}, "purpose": issue1996Purpose,
	})
	mu.Lock()
	if len(got) != 1 {
		mu.Unlock()
		t.Fatalf("the capturing receiver got %d requests, want 1", len(got))
	}
	sent := got[0]
	mu.Unlock()
	replay := func() int {
		res, _ := issue1870Post(t, baseURL(), source, sent.header.Get("Content-Type"), sent.body, map[string]string{
			"X-Signature": sent.header.Get("X-Signature"), "X-Timestamp": sent.header.Get("X-Timestamp"),
		})
		return res.StatusCode
	}
	if status := replay(); status != http.StatusAccepted {
		t.Fatalf("the signed request replayed within the tolerance was answered %d, want 202", status)
	}
	time.Sleep((tolerance + 1) * time.Second)
	if status := replay(); status != http.StatusUnauthorized {
		t.Fatalf("the same request replayed after the tolerance was answered %d, want 401", status)
	}
}

// TestIssue1996_TheSecretIsNeverShown reads the connection back, refuses a
// request that cannot be signed, and reads the session's audit events: none of
// them carries the signing secret.
func TestIssue1996_TheSecretIsNeverShown(t *testing.T) {
	c := connect(t)
	conn := issue1996Connection(t, c, issue1996SelfURL(), map[string]any{"hmac_preset": "github"})

	status, view := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the connection: HTTP %d: %v", status, view)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), issue1996Secret) || !strings.Contains(string(raw), "[REDACTED]") {
		t.Errorf("the connection read does not redact the signing secret: %s", raw)
	}

	res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/hooks/nothing", "purpose": issue1996Purpose,
	})
	if err != nil || res == nil || !res.IsError {
		t.Fatalf("a GET with nothing to sign was not refused: %v %s", err, text)
	}
	if !strings.Contains(text, "nothing to sign") || strings.Contains(text, issue1996Secret) {
		t.Errorf("the refusal = %q; it names the cause and never the secret", text)
	}

	events := c.list("/api/v1/admin/audit/events?session_id=" + c.sessionID + "&per_page=50")
	if len(events) == 0 {
		t.Fatal("the session has no audit events to read")
	}
	all, _ := json.Marshal(events)
	if strings.Contains(string(all), issue1996Secret) {
		t.Error("the audit log carries the signing secret")
	}
}

// TestIssue1996_APathSecretReachesAPathTokenSource registers a receiver whose
// secret is in its URL: the connection holds it as path_secret, which is
// appended as the request is sent and redacted when the connection is read.
func TestIssue1996_APathSecretReachesAPathTokenSource(t *testing.T) {
	c := connect(t)
	const token = "pathtoken1996secret"
	source := issue1870Name("path1996")
	issue1870Create(t, c, map[string]any{
		"name": source, "connection": issue1870Conn,
		"auth": map[string]any{"mode": "path_token", "secret": token}, "config": map[string]any{"event_id_path": "$.id"},
	})
	conn := issue1996Connection(t, c, issue1996SelfURL(), map[string]any{"auth_mode": "none", "credential": "", "path_secret": token})

	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "POST", "path": "/hooks/" + source,
		"body": map[string]any{"id": "path-" + source}, "purpose": issue1996Purpose,
	})
	if status, _ := out["status"].(float64); status != http.StatusAccepted {
		t.Fatalf("the path_token source answered %v: %v", out["status"], out)
	}
	if resolved, _ := out["resolved_path"].(string); strings.Contains(resolved, token) {
		t.Errorf("the call's path quotes the secret: %s", resolved)
	}
	_, view := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody)
	if raw, _ := json.Marshal(view); strings.Contains(string(raw), token) {
		t.Errorf("the connection read quotes the path secret: %s", raw)
	}
	if n := issue1996Events(t, c, source, "path-"+source); n != 1 {
		t.Errorf("%d events landed, want 1", n)
	}
}
