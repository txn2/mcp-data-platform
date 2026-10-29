//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2356: a secret an agent sends inside a tool call's
// arguments never reaches audit_logs. A stored secret is created through the
// built-in platform-admin connection with api_invoke_endpoint, its body sent
// as an object and as a string of JSON, the route named by path and by
// operation_id, and a webhook source with its secret
// nested in auth; the admin audit route is then read for the calls and
// searched for the values.
//
// Wire forms: api_invoke_endpoint takes body as an object and as a string of
// JSON, the route as method/path strings and as an operation_id string with a
// path_params object, connection/purpose as strings; the audit route is a GET
// with a query string.

const issue2356Purpose = "Acceptance for #2356: a secret in a tool call's arguments is redacted in the audit row."

func TestIssue2356_ASecretSentThroughPlatformAdminIsNotAudited(t *testing.T) {
	admin := connect(t)
	stamp := unique1579()
	forms := []struct {
		label string
		body  func(value string) any
	}{
		{"object", func(v string) any { return map[string]any{"value": v, "allow_connections": []string{fixture2051}} }},
		{"string of JSON", func(v string) any {
			return `{"value":"` + v + `","allow_connections":["` + fixture2051 + `"]}`
		}},
	}
	var values []string
	for i, f := range forms {
		for j, byOperation := range []bool{false, true} {
			suffix := string(rune('a'+i)) + string(rune('a'+j))
			name := "acc2356_" + strings.ToLower(stamp) + "_" + suffix
			value := "pw-2356-" + stamp + "-" + suffix
			values = append(values, value)
			t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
			args := map[string]any{"connection": "platform-admin", "body": f.body(value), "purpose": issue2356Purpose}
			if byOperation {
				args["operation_id"], args["path_params"] = "PUT /admin/secrets/{name}", map[string]any{"name": name}
			} else {
				args["method"], args["path"] = "PUT", "/api/v1/admin/secrets/"+name
			}
			res, text, err := admin.callRaw("api_invoke_endpoint", args)
			if err != nil || res.IsError {
				t.Fatalf("%s (operation_id %v): creating the secret through platform-admin: %v %s", f.label, byOperation, err, text)
			}
		}
	}

	source := issue1870Name("s2356")
	hookSecret := "whsec-2356-" + stamp
	values = append(values, hookSecret)
	res, text, err := admin.callRaw("api_invoke_endpoint", map[string]any{
		"connection": "platform-admin", "method": "POST", "path": "/api/v1/admin/webhooks/sources",
		"body": map[string]any{
			"name": source, "connection": issue1870Conn,
			"auth": map[string]any{"mode": "header_token", "header": "X-Hook", "secret": hookSecret},
		},
		"purpose": issue2356Purpose,
	})
	if err != nil || res.IsError {
		t.Fatalf("creating a webhook source through platform-admin: %v %s", err, text)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+source, http.NoBody) })

	deadline := time.Now().Add(30 * time.Second)
	for {
		rows := admin.list("/api/v1/admin/audit/events?tool_name=api_invoke_endpoint&per_page=100")
		raw, _ := json.Marshal(rows)
		seen := strings.Count(string(raw), "acc2356_"+strings.ToLower(stamp)) + strings.Count(string(raw), source)
		for _, v := range values {
			if strings.Contains(string(raw), v) {
				t.Fatalf("an audit row holds the secret %q", v)
			}
		}
		if seen >= len(values) {
			if !strings.Contains(string(raw), "[REDACTED]") {
				t.Fatalf("the calls are audited with no redaction marker: %s", raw)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the platform-admin calls did not reach the audit route within 30s")
		}
		time.Sleep(time.Second)
	}
}
