//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2406: the live schema of an application database is
// compared with what its applied migrations build, a difference raises an
// alert that resolves when it is undone, and an existing schema is adopted as
// a baseline. Each criterion saves an appdb connection of its own on the dev
// stack's orders database, so the loop's alerts for it are its alone and
// another criterion's schemas never reach them.
//
// Wire forms: manage_schema takes command, connection, schema, name and up as
// strings and version as an integer; the admin routes take the connection and
// schema in the path and the query, and a JSON object body whose sql is a
// string, params a list and read_only a boolean; the health settings take
// interval_seconds as a JSON number, channels as a list and each threshold as
// an object of two numbers. No parameter admits a second form.

const healthSettings2406 = "/api/v1/admin/appdb-health/settings"

// appdbConn saves an appdb connection on the target's orders database and
// returns its name.
func appdbConn(t *testing.T, admin *client, prefix, user, password string) string {
	t.Helper()
	conn := prefix + "-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{
		"host": "localhost", "port": targetPort(), "database": "orders", "user": user, "password": password,
		"sslmode": "disable", "max_conns": 4,
	}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance " + prefix})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving connection %s answered %d: %v", conn, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	return conn
}

// appdbPath is a route of an application database's operation.
func appdbPath(conn, rest string) string {
	return "/api/v1/admin/connection-instances/appdb/" + conn + rest
}

// console runs statements in the console and returns the answer.
func console(t *testing.T, c *client, conn, sql string, params []any, readOnly bool) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"sql": sql, "read_only": readOnly}
	if params != nil {
		body["params"] = params
	}
	return c.rest(http.MethodPost, appdbPath(conn, "/console"), jsonBody(t, body))
}

// consoleOK runs statements in the console and fails on anything but a clean
// answer.
func consoleOK(t *testing.T, c *client, conn, sql string, readOnly bool) map[string]any {
	t.Helper()
	status, out := console(t, c, conn, sql, nil, readOnly)
	if status != http.StatusOK || out["error"] != nil {
		t.Fatalf("console %q answered %d: %v", sql, status, out)
	}
	return out
}

// quietHealth is the health settings with every threshold off, sampling every
// ten seconds and posting to channel; a criterion turns on the one condition
// it is about.
func quietHealth(channel string) map[string]any {
	off := map[string]any{"warning": 0, "critical": 0}
	return map[string]any{
		"channels": []string{channel}, "interval_seconds": 10, "history_days": 1, "long_running_seconds": 60,
		"transaction_id_age": off, "idle_in_transaction_seconds": off, "connections_percent": off,
		"invalid_indexes": off, "dead_tuple_percent": off, "drift_objects": off,
	}
}

// migrate adds one migration to schema on conn and applies it.
func migrate(t *testing.T, c *client, conn, schema, up string) {
	t.Helper()
	call := func(args map[string]any) map[string]any {
		params := map[string]any{"connection": conn, "schema": schema}
		for k, v := range args {
			params[k] = v
		}
		return c.call("manage_schema", params)
	}
	call(map[string]any{"command": "add", "version": 1, "name": "start", "up": up})
	if done, _ := call(map[string]any{"command": "apply"})["done"].([]any); len(done) != 1 {
		t.Fatalf("applying the migration of %s on %s did not apply it", schema, conn)
	}
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
}

// differences reads a drift result's differences as "kind object change".
func differences(result any) []string {
	r, _ := result.(map[string]any)
	out := []string{}
	diffs, _ := r["differences"].([]any)
	for _, d := range diffs {
		m, _ := d.(map[string]any)
		out = append(out, fmt.Sprintf("%v %v %v", m["kind"], m["object"], m["change"]))
	}
	return out
}

// appdbToolsUnchanged is the criterion every #2414 child carries: the appdb
// kind registers manage_schema alone, for every persona.
func appdbToolsUnchanged(t *testing.T) {
	t.Helper()
	for _, key := range []string{devAPIKey(), "acme-owner-key", "acme-analyst-key"} {
		c := connectAs(t, key)
		for _, tool := range c.tools() {
			name := tool.Name
			for _, word := range []string{"appdb", "postgres_", "maintain", "console", "drift", "adopt", "db_health"} {
				if strings.Contains(name, word) {
					t.Errorf("tools/list for %s holds %s", key, name)
				}
			}
		}
	}
	admin := connect(t)
	found := false
	for _, tool := range admin.tools() {
		found = found || tool.Name == "manage_schema"
	}
	if !found {
		t.Error("tools/list for the administrator does not hold manage_schema")
	}
}

func TestIssue2406_AColumnAddedByHandIsDriftAndAnAlertThatResolves(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	conn := appdbConn(t, admin, "acc-2406-drift", "orders_app", "orders_secret")
	schema := "acc2406_drift_" + unique1579()
	migrate(t, admin, conn, schema, "CREATE TABLE t (id bigint PRIMARY KEY)")

	channel := "acc-2406-" + unique1579()
	env := issue2148Channel(t, admin, channel)
	body := quietHealth(channel)
	body["drift_objects"] = map[string]any{"warning": 1, "critical": 0}
	putSettings(t, admin, healthSettings2406, body)

	consoleOK(t, admin, conn, "ALTER TABLE "+schema+".t ADD COLUMN coupon text", false)
	want := "column t.coupon added"

	status := admin.call("manage_schema", map[string]any{"command": "status", "connection": conn, "schema": schema})
	if got := differences(status["schema_drift"]); len(got) != 1 || got[0] != want {
		t.Fatalf("manage_schema status reports drift %v, want [%s]: %v", got, want, status["schema_drift"])
	}
	code, page := admin.rest(http.MethodGet, appdbPath(conn, "/drift?schema="+schema), http.NoBody)
	schemas, _ := page["schemas"].([]any)
	if code != http.StatusOK || len(schemas) != 1 {
		t.Fatalf("the drift route answered %d: %v", code, page)
	}
	if got := differences(schemas[0]); len(got) != 1 || got[0] != want {
		t.Fatalf("the drift route reports %v, want [%s]", got, want)
	}

	title := "Application database " + conn + ": 1 object(s) in schema " + schema + " differ"
	waitFor(t, "the drift alert", func() (bool, string) {
		n := issue2148Count(t, env, "WARNING: "+title, "column "+schema+".t.coupon added")
		return n == 1, fmt.Sprintf("%d warning posts", n)
	})

	consoleOK(t, admin, conn, "ALTER TABLE "+schema+".t DROP COLUMN coupon", false)
	waitFor(t, "the resolution", func() (bool, string) {
		n := issue2148Count(t, env, "Resolved: "+title)
		return n == 1, fmt.Sprintf("%d resolved posts", n)
	})
	status = admin.call("manage_schema", map[string]any{"command": "status", "connection": conn, "schema": schema})
	if got := differences(status["schema_drift"]); len(got) != 0 {
		t.Errorf("drift after dropping the column: %v", got)
	}
}

func TestIssue2406_AdoptingTheSeededSchemaWritesABaselineThatBuildsWithoutDrift(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	conn := appdbConn(t, admin, "acc-2406-adopt", "orders_app", "orders_secret")
	seed, err := os.ReadFile("../../dev/fixtures/postgres-target.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's schema, built again in a schema of its own so the
	// criterion runs on every stack and adopts what the stack seeds.
	schema := "acc2406_seed_" + unique1579()
	targetPSQL(t, "CREATE SCHEMA "+schema+"; SET search_path = "+schema+";\n"+string(seed))
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })

	code, res := admin.rest(http.MethodPost, appdbPath(conn, "/adopt?schema="+schema), http.NoBody)
	if code != http.StatusOK || res["adopted"] != true {
		t.Fatalf("adopting answered %d: %v", code, res)
	}
	up, _ := res["up"].(string)
	for _, table := range []string{"customers", "orders", "type_samples"} {
		if !strings.Contains(up, "CREATE TABLE "+schema+"."+table+" (") {
			t.Errorf("the baseline does not create %s.%s:\n%s", schema, table, up)
		}
	}
	status := admin.call("manage_schema", map[string]any{"command": "status", "connection": conn, "schema": schema})
	if status["current_version"] != float64(1) {
		t.Errorf("the ledger is at %v after adopting, want 1: %v", status["current_version"], status)
	}
	drift, _ := status["schema_drift"].(map[string]any)
	if drift["checked"] != true || len(differences(drift)) != 0 {
		t.Errorf("a throwaway database built from the baseline differs from the original: %v", drift)
	}
	history := admin.call("manage_schema", map[string]any{"command": "history", "connection": conn, "schema": schema})
	if !strings.Contains(asJSON(history), "adopted from the existing schema") {
		t.Errorf("the history does not record the adoption: %v", history)
	}
	code, again := admin.rest(http.MethodPost, appdbPath(conn, "/adopt?schema="+schema), http.NoBody)
	if code != http.StatusConflict {
		t.Errorf("adopting a managed schema again answered %d: %v", code, again)
	}
}

func TestIssue2406_ANonAdministratorIsRefusedEveryRoute(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2406-authz", "orders_app", "orders_secret")
	owner := connectAs(t, "acme-owner-key")
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, appdbPath(conn, "/drift")},
		{http.MethodPost, appdbPath(conn, "/adopt?schema=public")},
		{http.MethodGet, healthSettings2406},
	} {
		if code, body := owner.rest(r.method, r.path, http.NoBody); code != http.StatusForbidden {
			t.Errorf("%s %s as a collaborator answered %d: %v", r.method, r.path, code, body)
		}
	}
}

func TestIssue2406_TheAppDBKindRegistersManageSchemaAlone(t *testing.T) {
	appdbToolsUnchanged(t)
}
