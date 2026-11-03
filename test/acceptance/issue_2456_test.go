//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Acceptance for #2456: manage_schema's outbox template with notify false
// writes a capture function that sends no notification, and the webhook
// source's poll alone delivers its rows; without notify the function still
// notifies; notify without the template is refused. Each criterion keeps its
// tables in a schema of its own on the dev stack's `orders` application
// database and reuses #2408's source and script helpers.
//
// Wire forms: manage_schema add takes template "outbox", capture as a JSON
// array of strings and notify as a JSON boolean (true and false are both
// sent); the admin source route takes config.postgres as a JSON object with
// poll_seconds a JSON number. No parameter admits a second form.

// outbox2456 builds a schema holding an orders table captured by an outbox
// added with the template's further arguments, and returns the schema.
func outbox2456(t *testing.T, admin *client, extra map[string]any) string {
	t.Helper()
	schema := "acc2456_" + unique1579()
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "orders",
		"up": "CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)",
	})
	tmpl := map[string]any{"command": "add", "version": 2, "template": "outbox", "capture": []string{"orders"}}
	for k, v := range extra {
		tmpl[k] = v
	}
	schemaCall(admin, schema, tmpl)
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	return schema
}

// captureDef2456 is the definition of schema's outbox capture function.
func captureDef2456(t *testing.T, schema string) string {
	t.Helper()
	return targetPSQL(t, "SELECT pg_get_functiondef('"+schema+".outbox_capture'::regproc)")
}

func TestIssue2456_AnOutboxWithoutNotifyIsDeliveredByThePollAlone(t *testing.T) {
	admin := connect(t)
	schema := outbox2456(t, admin, map[string]any{"notify": false})
	if def := captureDef2456(t, schema); strings.Contains(def, "pg_notify") || !strings.Contains(def, "INSERT INTO") {
		t.Fatalf("the capture function must write the row and notify nothing: %s", def)
	}
	source, script := source2408(t, admin, "acc-2456-quiet", map[string]any{
		"connection": conn2404, "mode": "outbox", "table": schema + ".outbox", "poll_seconds": 3,
	})
	listening2408(t, admin, source)

	targetPSQL(t, "INSERT INTO "+schema+".orders VALUES (11, 4.25)")
	p := payloads2408(t, admin, script, 1)[0]
	row, _ := p["row"].(map[string]any)
	if p["op"] != "INSERT" || fmt.Sprint(row["id"]) != "11" || row["total"] != 4.25 {
		t.Errorf("the poll must deliver the inserted row: %v", p)
	}
	if n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".outbox")); n != "0" {
		t.Errorf("the outbox holds %s rows after they were filed; want none", n)
	}
}

func TestIssue2456_AnOutboxAddedWithoutNotifyOrWithNotifyTrueStillNotifies(t *testing.T) {
	admin := connect(t)
	for name, extra := range map[string]map[string]any{
		"absent": nil,
		"true":   {"notify": true},
	} {
		schema := outbox2456(t, admin, extra)
		if def := captureDef2456(t, schema); !strings.Contains(def, "pg_notify(") {
			t.Errorf("notify %s: the capture function must notify the outbox channel: %s", name, def)
		}
	}
}

func TestIssue2456_NotifyWithoutTheOutboxTemplateIsRefused(t *testing.T) {
	admin := connect(t)
	schema := "acc2456_" + unique1579()
	text := schemaRefused(t, admin, schema, map[string]any{
		"command": "add", "version": 1, "name": "orders", "notify": false,
		"up": "CREATE TABLE orders (id int PRIMARY KEY)", "down": "DROP TABLE orders",
	})
	if !strings.Contains(text, "notify") || !strings.Contains(text, "template outbox") {
		t.Errorf("the refusal must name notify and the outbox template: %s", text)
	}
}
