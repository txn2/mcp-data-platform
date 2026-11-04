//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2427: an administrator's console on an application
// database. A SELECT reads the rows; with read only on an UPDATE is refused by
// the database and changes nothing; with it off the UPDATE commits, and its
// audit row records the SQL text and the bound parameter's type, never its
// value; a non-administrator is answered 403.
//
// Wire forms: the console takes a JSON object whose sql is a string, params a
// list (positional) or an object (named), and read_only a boolean; both
// params forms are sent. No other parameter admits a second form.

func TestIssue2427_AnAdministratorReadsRows(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2427-read", "orders_app", "orders_secret")
	out := consoleOK(t, admin, conn, "SELECT email FROM public.customers ORDER BY id", true)
	if !strings.Contains(asJSON(out["results"]), "ada@acme.example") {
		t.Fatalf("the console did not return the seeded rows: %v", out)
	}
	code, named := admin.rest(http.MethodPost, appdbPath(conn, "/console"), jsonBody(t, map[string]any{
		"sql": "SELECT name FROM public.customers WHERE email = @email", "params": map[string]any{"email": "grace@acme.example"},
	}))
	if code != http.StatusOK || !strings.Contains(asJSON(named), "Grace Hopper") {
		t.Fatalf("named params answered %d: %v", code, named)
	}
}

func TestIssue2427_AnUpdateIsRefusedReadOnlyAndCommitsWithTheToggleOff(t *testing.T) {
	admin := connectFor(t, 3*time.Minute)
	conn := appdbConn(t, admin, "acc-2427-write", "orders_app", "orders_secret")
	schema := "acc2427_" + unique1579()
	targetPSQL(t, "CREATE SCHEMA "+schema+"; CREATE TABLE "+schema+".notes (id int PRIMARY KEY, body text); INSERT INTO "+schema+".notes VALUES (1, 'before')")
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	k := newCanary2363(t)
	value := k.plant("console-bound-value")
	update := "UPDATE " + schema + ".notes SET body = $1 WHERE id = 1"

	code, refused := console(t, admin, conn, update, []any{value}, true)
	if code != http.StatusOK || !strings.Contains(asJSON(refused["error"]), "25006") || refused["committed"] != false {
		t.Fatalf("an UPDATE read only answered %d: %v", code, refused)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT body FROM "+schema+".notes WHERE id = 1")); got != "before" {
		t.Fatalf("the refused UPDATE changed the row to %q", got)
	}

	code, committed := console(t, admin, conn, update, []any{value}, false)
	if code != http.StatusOK || committed["committed"] != true || committed["error"] != nil {
		t.Fatalf("the UPDATE with the toggle off answered %d: %v", code, committed)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT body FROM "+schema+".notes WHERE id = 1")); got != value {
		t.Fatalf("the committed UPDATE left the row %q", got)
	}

	var row map[string]any
	waitFor(t, "the console's audit row", func() (bool, string) {
		rows := admin.list("/api/v1/admin/audit/events?tool_name=appdb_console&per_page=50")
		for _, r := range rows {
			if strings.Contains(asJSON(r), update) && strings.Contains(asJSON(r), `"committed":true`) {
				row, _ = r.(map[string]any)
				return true, ""
			}
		}
		return false, "no committed console row names the statement"
	})
	raw := asJSON(row)
	if row["source"] != "admin_console" || !strings.Contains(raw, `"type":"string"`) {
		t.Errorf("the audit row does not carry the source and the parameter's type: %s", raw)
	}
	if strings.Contains(raw, value) {
		t.Errorf("the audit row carries the bound value: %s", raw)
	}
	k.searchDatabase()
	k.searchFiles()
}

func TestIssue2427_ANonAdministratorIsAnswered403(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2427-authz", "orders_app", "orders_secret")
	for _, key := range []string{"acme-owner-key", "acme-analyst-key"} {
		c := connectAs(t, key)
		if code, body := c.rest(http.MethodPost, appdbPath(conn, "/console"), jsonBody(t, map[string]any{"sql": "SELECT 1"})); code != http.StatusForbidden {
			t.Errorf("the console as %s answered %d: %v", key, code, body)
		}
	}
}

func TestIssue2427_TheAppDBKindRegistersManageSchemaAlone(t *testing.T) {
	appdbToolsUnchanged(t)
}
