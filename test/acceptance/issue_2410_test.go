//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2410: maintenance from the admin API (analyze sets a
// table's last analyze, vacuum clears its dead tuples), terminate on a
// session of the connection's own role and never another role's, a saved
// script calling platform.maintain for an administrator and refused for a
// persona without the grant, and an index change refused naming
// manage_schema by the route and the binding alike.
//
// Wire forms: the maintenance route takes command and table as strings and
// timeout_seconds as an integer in a JSON object; the backend routes take the
// pid and the signal in the path; manage_script takes command, name and
// source as strings, run_script name as a string and wait_seconds as an
// integer, and platform.maintain takes connection, command and table as
// strings. No parameter admits a second form.

// maintain runs a command from the admin API.
func maintain(t *testing.T, c *client, conn string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, appdbPath(conn, "/maintenance"), jsonBody(t, body))
}

// targetStat reads one column of pg_stat_user_tables for schema.table.
func targetStat(t *testing.T, schema, table, column string) string {
	t.Helper()
	return strings.TrimSpace(targetPSQL(t, fmt.Sprintf(
		"SELECT coalesce(%s::text, '') FROM pg_stat_user_tables WHERE schemaname = '%s' AND relname = '%s'", column, schema, table)))
}

func TestIssue2410_AnalyzeSetsLastAnalyzeAndVacuumClearsDeadTuples(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	conn := appdbConn(t, admin, "acc-2410-maint", "orders_app", "orders_secret")
	schema := "acc2410_" + unique1579()
	migrate(t, admin, conn, schema, "CREATE TABLE items (id bigint PRIMARY KEY, sku text) WITH (autovacuum_enabled = false)")
	targetPSQL(t, "INSERT INTO "+schema+".items SELECT g, 'x' FROM generate_series(1, 2000) g; DELETE FROM "+schema+".items WHERE id <= 1000")
	if before := targetStat(t, schema, "items", "last_analyze"); before != "" {
		t.Fatalf("the table was analyzed before the criterion: %s", before)
	}

	code, out := maintain(t, admin, conn, map[string]any{"command": "analyze", "table": schema + ".items"})
	if code != http.StatusOK || out["statement"] != `ANALYZE "`+schema+`"."items"` {
		t.Fatalf("analyze answered %d: %v", code, out)
	}
	waitFor(t, "last_analyze", func() (bool, string) {
		v := targetStat(t, schema, "items", "last_analyze")
		return v != "", v
	})
	waitFor(t, "the dead tuples to be counted", func() (bool, string) {
		v := targetStat(t, schema, "items", "n_dead_tup")
		return v == "1000", v
	})
	code, out = maintain(t, admin, conn, map[string]any{"command": "vacuum", "table": schema + ".items", "timeout_seconds": 60})
	if code != http.StatusOK {
		t.Fatalf("vacuum answered %d: %v", code, out)
	}
	waitFor(t, "vacuum to clear the dead tuples", func() (bool, string) {
		v := targetStat(t, schema, "items", "n_dead_tup")
		return v == "0", v
	})
}

func TestIssue2410_TerminateEndsTheConnectionsOwnSessionAndRefusesAnotherRoles(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2410-signal", "orders_app", "orders_secret")
	alive := func(pid int64) bool {
		return strings.TrimSpace(targetPSQL(t, fmt.Sprintf("SELECT count(*) FROM pg_stat_activity WHERE pid = %d", pid))) == "1"
	}

	pid, _ := idleSession2409(t, "orders_app", "orders_secret", "SELECT 1")
	code, out := admin.rest(http.MethodPost, appdbPath(conn, fmt.Sprintf("/backends/%d/terminate", pid)), http.NoBody)
	if code != http.StatusOK || out["sent"] != true {
		t.Fatalf("terminate answered %d: %v", code, out)
	}
	waitFor(t, "the session to end", func() (bool, string) { return !alive(pid), "still alive" })

	role := "acc2410_other_" + unique1579()
	targetPSQL(t, "CREATE ROLE "+role+" LOGIN PASSWORD 'other-pw'")
	t.Cleanup(func() { targetPSQL(t, "DROP ROLE IF EXISTS "+role) })
	other, _ := idleSession2409(t, role, "other-pw", "SELECT 1")
	code, out = admin.rest(http.MethodPost, appdbPath(conn, fmt.Sprintf("/backends/%d/terminate", other)), http.NoBody)
	if code != http.StatusBadRequest || !strings.Contains(asJSON(out), "not the platform's to terminate") {
		t.Fatalf("terminating another role's backend answered %d: %v", code, out)
	}
	if !alive(other) {
		t.Error("another role's backend was ended")
	}

	owner := connectAs(t, "acme-owner-key")
	if code, body := owner.rest(http.MethodPost, appdbPath(conn, fmt.Sprintf("/backends/%d/cancel", other)), http.NoBody); code != http.StatusForbidden {
		t.Errorf("a collaborator's cancel answered %d: %v", code, body)
	}
	if code, body := owner.rest(http.MethodPost, appdbPath(conn, "/maintenance"), jsonBody(t, map[string]any{"command": "analyze"})); code != http.StatusForbidden {
		t.Errorf("a collaborator's maintenance answered %d: %v", code, body)
	}
}

// maintainSource is a script that analyzes the orders database.
const maintainSource2410 = `def main():
    """Analyzes the orders database."""
    done = platform.maintain("orders", "analyze")
    print(done.statement)
`

func TestIssue2410_ASavedScriptMaintainsForAnAdministratorAndNotForAPersonaWithoutTheGrant(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	name := "acc-2410-script-" + unique1579()
	created := admin.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2410: maintenance from a script.", "source": maintainSource2410,
	}, nil)
	if created["status"] != "created" {
		t.Fatalf("manage_script create: %v", created)
	}
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	run := admin.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if run["status"] != "succeeded" || !strings.Contains(fmt.Sprint(run["log"]), "ANALYZE") {
		t.Fatalf("the administrator's run did not maintain: %v", run)
	}
	runID, _ := run["run_id"].(string)
	waitFor(t, "the run's audit row", func() (bool, string) {
		code, events := admin.rest(http.MethodGet, "/api/v1/admin/audit/events?event_kind=script_run&session_id="+runID, http.NoBody)
		return code == http.StatusOK && strings.Contains(asJSON(events), `"run_id":"`+runID+`"`), fmt.Sprintf("%d %v", code, events)
	})

	// A collaborator's draft is refused at the binding, so no recording of
	// it reaches the print; the same source is saved with a test that
	// declares the binding's answer.
	owner := connectAs(t, "acme-owner-key")
	ownName := "acc-2410-owner-" + unique1579()
	created = owner.call("manage_script", map[string]any{
		"command": "create", "name": ownName, "description": "Acceptance #2410: maintenance without the grant.",
		"source": maintainSource2410 + `
def test_prints_what_ran():
    """The binding's statement is printed."""
    testing.answer("appdb.maintain", {"connection": "orders", "command": "analyze"}, {"statement": "ANALYZE", "duration_ms": 1})
    main()
    assert.eq(testing.outputs().log.strip(), "ANALYZE")
`,
	})
	if created["status"] != "created" {
		t.Fatalf("manage_script create as the collaborator: %v", created)
	}
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": ownName}) })
	run = owner.call("run_script", map[string]any{"name": ownName, "wait_seconds": 120})
	if run["status"] != "failed" || !strings.Contains(asJSON(run), "schema_apply_personas") {
		t.Fatalf("a run authored by a persona without the grant did not fail naming it: %v", run)
	}
}

func TestIssue2410_AnIndexChangeIsRefusedNamingManageSchema(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2410-index", "orders_app", "orders_secret")
	code, out := maintain(t, admin, conn, map[string]any{"command": "create_index", "table": "public.orders"})
	if code != http.StatusBadRequest || !strings.Contains(asJSON(out), "manage_schema") {
		t.Fatalf("an index change from the admin API answered %d: %v", code, out)
	}
	draft := draft2428(admin, "acc-2410-index-draft", `def main():
    """Asks maintenance for an index."""
    out = platform.maintain("orders", "create_index", table = "public.orders", on_error = "return")
    print(json.encode(out))
`, true)
	if !strings.Contains(asJSON(draft), "manage_schema") {
		t.Fatalf("the binding's refusal does not name manage_schema: %v", draft)
	}
}

func TestIssue2410_TheAppDBKindRegistersManageSchemaAlone(t *testing.T) {
	appdbToolsUnchanged(t)
}
