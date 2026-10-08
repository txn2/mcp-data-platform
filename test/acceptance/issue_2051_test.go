//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #2051: a stored secret a request references as {{secret:<name>}}, filled
// in by the api gateway as it sends the request to the api-test fixture's
// /v1/echo, which answers with what it received, and redacted from what
// comes back.
//
// Wire forms: api_invoke_endpoint's body admits an object and a string of
// JSON (#1548); each is sent as literal params with the placeholder inside,
// and both must reach the fixture filled and come back redacted. headers and
// query_params are string maps, sent once each with a placeholder value. The
// admin route's body is one JSON object; its allow_personas is sent as an
// empty list, as null and as a list. A managed script's platform.call
// arguments are the object form, sent from a run_draft whose allow_writes is
// a boolean.

const fixture2051 = "api-test-fixture"

// secret2051 stores a secret through the admin API and removes it after the
// test, returning its name and value.
func secret2051(t *testing.T, admin *client, personas []string, connections ...string) (name, value string) {
	t.Helper()
	stamp := unique1579()
	name = "acc2051_" + strings.ToLower(stamp)
	value = "pw-" + stamp + "-Zq!"
	status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "Acceptance #2051", "value": value,
		"allow_connections": connections, "allow_personas": personas,
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	return name, value
}

func TestIssue2051_TheUpstreamReceivesTheValueAndTheCallerSeesItRedacted(t *testing.T) {
	admin := connect(t)
	name, value := secret2051(t, admin, []string{}, fixture2051)
	ph := "{{secret:" + name + "}}"
	forms := []struct {
		label string
		body  any
	}{
		{"object", map[string]any{"text": ph}},
		{"string of JSON", `{"text": "` + ph + `"}`},
	}
	for _, form := range forms {
		t.Run(form.label, func(t *testing.T) {
			out := admin.call("api_invoke_endpoint", map[string]any{
				"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
				"body":         form.body,
				"headers":      map[string]string{"X-Portal-Login": ph},
				"query_params": map[string]any{"login": ph},
				"purpose":      "Acceptance #2051: a stored secret reaches the upstream and not the caller.",
			})
			text := stringOf(out)
			if strings.Contains(text, value) {
				t.Fatalf("the caller was handed the value: %v", out)
			}
			// The fixture echoes what it received. The redaction stands where
			// the value was, so the value arrived; the placeholder does not,
			// so nothing was sent as written.
			echoed, _ := out["body"].(map[string]any)
			body := stringOf(echoed["body"]) + stringOf(echoed["headers"]) + stringOf(echoed["query"])
			if got := strings.Count(body, "[REDACTED:"+name+"]"); got < 3 {
				t.Errorf("the echo shows the redaction %d times, want the body, header and query each: %v", got, echoed)
			}
			if strings.Contains(body, ph) {
				t.Errorf("the upstream received the placeholder as written: %v", echoed)
			}
		})
	}
}

const script2051 = `
def main():
    """Types the stored password into the portal's login field."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture", "method": "POST", "path": "/v1/echo",
        "body": {"text": "{{secret:%s}}"},
        "purpose": "Acceptance #2051: a script sends a stored secret.",
    })
    print(json.encode(res["body"]["body"]))
`

func TestIssue2051_AScriptRecordingAndTheAuditHoldOnlyThePlaceholder(t *testing.T) {
	admin := connect(t)
	name, value := secret2051(t, admin, nil, fixture2051)
	source := strings.Replace(script2051, "%s", name, 1)
	ran := admin.call("manage_script", map[string]any{"command": "run_draft", "name": "acceptance-2051-" + unique1579(), "source": source, "allow_writes": true})
	if ran["status"] != "succeeded" {
		t.Fatalf("the draft failed: %v", ran)
	}
	log, _ := ran["log"].(string)
	if !strings.Contains(log, "[REDACTED:"+name+"]") || strings.Contains(log, value) {
		t.Errorf("the run printed %q; want the echoed value redacted", log)
	}
	recording, _ := ran["recording"].(string)
	if recording == "" {
		t.Fatalf("the draft kept no recording: %v", ran)
	}

	data := recording2051(t, recording)
	if bytes.Contains(data, []byte(value)) || !bytes.Contains(data, []byte("{{secret:"+name+"}}")) {
		t.Errorf("the recording holds the value, or not the placeholder")
	}

	var rows []any
	for attempt := 0; attempt < 40 && len(rows) == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		for _, row := range admin.list("/api/v1/admin/audit/events?per_page=200&tool_name=api_invoke_endpoint&session_id=" + recording) {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		t.Fatalf("no audit row for the run's call")
	}
	audit := stringOf(rows)
	if strings.Contains(audit, value) || !strings.Contains(audit, "{{secret:"+name+"}}") {
		t.Errorf("the audit row holds the value, or not the placeholder: %s", audit)
	}
}

// recording2051 reads a recording's calls from the dev database, unzipped.
func recording2051(t *testing.T, runID string) []byte {
	t.Helper()
	db := issue1904DB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var data []byte
	if err := db.QueryRowContext(ctx, `SELECT data FROM script_recordings WHERE run_id = $1`, runID).Scan(&data); err != nil {
		t.Fatalf("reading recording %s: %v", runID, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("opening the recording: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading the recording: %v", err)
	}
	return out
}

func TestIssue2051_RefusedOutsideItsScopeOrUnknownAndNothingIsSent(t *testing.T) {
	admin := connect(t)
	other := "acc-2051-other-" + strings.ToLower(unique1579())
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+other, map[string]any{
		"config": fixtureConnectionConfig(other, ""), "description": "Acceptance #2051: a connection a secret does not name.",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating the connection answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+other, http.NoBody) })
	name, _ := secret2051(t, admin, []string{"admin"}, fixture2051)
	owner := connectAs(t, devOwnerAPIKey)

	cases := []struct {
		label  string
		caller *client
		conn   string
		secret string
		want   string
	}{
		{"connection outside allow_connections", admin, other, name, `may not be sent through connection "` + other + `"`},
		{"unknown name", admin, fixture2051, "acc2051_no_such_secret", `secret "acc2051_no_such_secret" does not exist`},
		{"persona outside allow_personas", owner, fixture2051, name, "may not be used by persona"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			res, text, err := c.caller.callRaw("api_invoke_endpoint", map[string]any{
				"connection": c.conn, "method": http.MethodPost, "path": "/v1/echo",
				"body":    map[string]any{"text": "{{secret:" + c.secret + "}}"},
				"purpose": "Acceptance #2051: a placeholder outside its scope is refused.",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || !strings.Contains(text, c.want) || !strings.Contains(text, "nothing was sent") {
				t.Errorf("the call was not refused by name: %s", text)
			}
		})
	}
}

func TestIssue2051_TheAdminAPINeverReturnsTheValue(t *testing.T) {
	admin := connect(t)
	name, value := secret2051(t, admin, []string{"admin"}, fixture2051)
	for _, path := range []string{"/api/v1/admin/secrets/" + name, "/api/v1/admin/secrets"} {
		status, body := admin.rest(http.MethodGet, path, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET %s answered %d", path, status)
		}
		if text := stringOf(body); strings.Contains(text, value) || !strings.Contains(text, name) {
			t.Errorf("GET %s returned the value, or not the secret: %v", path, body)
		}
	}
	// A change without a value keeps the stored one: the next call still
	// sends it.
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "rescoped", "allow_connections": []string{fixture2051},
	}); status != http.StatusOK {
		t.Fatalf("changing the secret answered %d", status)
	}
	out := admin.call("api_invoke_endpoint", map[string]any{
		"connection": fixture2051, "method": http.MethodPost, "path": "/v1/echo",
		"body":    map[string]any{"text": "{{secret:" + name + "}}"},
		"purpose": "Acceptance #2051: a rescoped secret keeps its value.",
	})
	if !strings.Contains(stringOf(out), "[REDACTED:"+name+"]") {
		t.Errorf("after the change the upstream did not receive the stored value: %v", out)
	}
}
