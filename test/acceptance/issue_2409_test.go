//go:build integration

package acceptance

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq" // the criterion holds a session idle in a transaction on the target
)

// Acceptance for #2409: an application database's health from its own
// statistics views on the connection page's route, the unused index as a
// manage_schema describe finding, a session idle in a transaction listed and
// alerted at its level, a report that says when pg_stat_statements is not
// installed, and the route refused to anyone but an administrator. What a
// session's statement says and the connection's password reach no stored
// row, log, span or alert.
//
// Wire forms: manage_schema takes command, connection, schema, name and up as
// strings and version as an integer; the health route takes the connection in
// its path, the history route days as a query integer, and the settings a
// JSON object of numbers, a list and threshold objects. No parameter admits a
// second form.

// idleSession2409 opens a session on the target as user and leaves it idle
// in a transaction after running statement; closing it ends the transaction.
func idleSession2409(t *testing.T, user, password, statement string) (pid int64, closeIt func()) {
	t.Helper()
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(user, password), Host: "localhost:" + targetPort(),
		Path: "/orders", RawQuery: "sslmode=disable&application_name=acc-2409",
	}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("opening a session as %s: %v", user, err)
	}
	if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(statement); err != nil {
		t.Fatal(err)
	}
	closed := false
	closeIt = func() {
		if !closed {
			closed = true
			_ = tx.Rollback()
			_ = db.Close()
		}
	}
	t.Cleanup(closeIt)
	return pid, closeIt
}

// health reads the health report.
func health(t *testing.T, c *client, conn string) map[string]any {
	t.Helper()
	code, out := c.rest(http.MethodGet, appdbPath(conn, "/health"), http.NoBody)
	if code != http.StatusOK {
		t.Fatalf("the health route answered %d: %v", code, out)
	}
	return out
}

// tableStats is a table's entry in a health report.
func tableStats(report map[string]any, schema, name string) map[string]any {
	tables, _ := report["tables"].([]any)
	for _, x := range tables {
		m, _ := x.(map[string]any)
		if m["schema"] == schema && m["name"] == name {
			return m
		}
	}
	return nil
}

func TestIssue2409_DeadTuplesAnUnusedIndexAndAnIdleSessionWithItsAlert(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	conn := appdbConn(t, admin, "acc-2409-health", "orders_app", "orders_secret")
	schema := "acc2409_" + unique1579()
	migrate(t, admin, conn, schema, "CREATE TABLE items (id bigint PRIMARY KEY, sku text NOT NULL) WITH (autovacuum_enabled = false);\nCREATE INDEX items_sku ON items (sku)")
	targetPSQL(t, "INSERT INTO "+schema+".items SELECT g, 'sku-' || g FROM generate_series(1, 3000) g; DELETE FROM "+schema+".items WHERE id <= 1500")

	var report map[string]any
	waitFor(t, "the dead tuples", func() (bool, string) {
		report = health(t, admin, conn)
		st := tableStats(report, schema, "items")
		dead, _ := st["dead_tuples"].(float64)
		return dead >= 1500, fmt.Sprintf("items: %v", st)
	})
	unused := asJSON(report["indexes"])
	if !strings.Contains(unused, `"name":"items_sku"`) {
		t.Errorf("the index never scanned is not listed unused: %s", unused)
	}
	described := admin.call("manage_schema", map[string]any{"command": "describe", "connection": conn, "schema": schema})
	if f := asJSON(described["findings"]); !strings.Contains(f, `"kind":"unused_index"`) || !strings.Contains(f, `"index":"items_sku"`) {
		t.Errorf("describe has no unused_index finding for items_sku: %s", f)
	}

	channel := "acc-2409-" + unique1579()
	env := issue2148Channel(t, admin, channel)
	body := quietHealth(channel)
	body["idle_in_transaction_seconds"] = map[string]any{"warning": 5, "critical": 0}
	putSettings(t, admin, healthSettings2406, body)

	pid, closeIdle := idleSession2409(t, "orders_app", "orders_secret", "SELECT 1")
	waitFor(t, "the idle session in the report", func() (bool, string) {
		sessions := asJSON(health(t, admin, conn)["sessions"])
		return strings.Contains(sessions, fmt.Sprintf(`"pid":%d`, pid)) && strings.Contains(sessions, "idle in transaction"), sessions
	})
	title := "Application database " + conn + ": a session has been idle in a transaction"
	waitFor(t, "the idle-in-transaction warning", func() (bool, string) {
		n := issue2148Count(t, env, "WARNING: "+title)
		return n >= 1, fmt.Sprintf("%d warning posts", n)
	})
	closeIdle()
	waitFor(t, "the resolution", func() (bool, string) {
		n := issue2148Count(t, env, "Resolved: "+title)
		return n >= 1, fmt.Sprintf("%d resolved posts", n)
	})

	code, history := admin.rest(http.MethodGet, appdbPath(conn, "/health/history?days=1"), http.NoBody)
	if points, _ := history["points"].([]any); code != http.StatusOK || len(points) == 0 {
		t.Errorf("the history holds no sample after the loop ran: %d %v", code, history)
	}
}

func TestIssue2409_WithoutPgStatStatementsTheReportSaysSoAndIsComplete(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2409-pgss", "orders_app", "orders_secret")
	report := health(t, admin, conn)
	statements, _ := report["statements"].(map[string]any)
	if statements["installed"] != false || !strings.Contains(fmt.Sprint(statements["note"]), "not installed") {
		t.Fatalf("the statements section does not say pg_stat_statements is not installed: %v", statements)
	}
	if errs, _ := report["errors"].([]any); len(errs) != 0 {
		t.Errorf("the rest of the report is not complete: %v", errs)
	}
	server, _ := report["server"].(map[string]any)
	if !strings.HasPrefix(fmt.Sprint(server["version"]), "17") || server["database"] != "orders" {
		t.Errorf("the server section reads %v", server)
	}
	if tableStats(report, "public", "orders") == nil {
		t.Error("the report lists no public.orders table")
	}
}

func TestIssue2409_ANonAdministratorIsRefusedTheHealthRoute(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2409-authz", "orders_app", "orders_secret")
	owner := connectAs(t, "acme-owner-key")
	for _, path := range []string{appdbPath(conn, "/health"), appdbPath(conn, "/health/history")} {
		if code, body := owner.rest(http.MethodGet, path, http.NoBody); code != http.StatusForbidden {
			t.Errorf("GET %s as a collaborator answered %d: %v", path, code, body)
		}
	}
}

// What a session's statement says is shown to the administrator who reads the
// report and kept nowhere; the connection's login password is never read
// back. Both are searched through every sink after the loop has sampled and
// alerted.
func TestIssue2409_StatementTextAndThePasswordReachNoSink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	password := k.plant("appdb-login-password")
	role := "acc2409_" + k.stamp[3:11]
	targetPSQL(t, fmt.Sprintf("CREATE ROLE %s LOGIN SUPERUSER PASSWORD '%s'", role, password))
	t.Cleanup(func() { targetPSQL(t, "DROP OWNED BY "+role+"; DROP ROLE IF EXISTS "+role) })
	conn := appdbConn(t, admin, "acc-2409-sinks", role, password)

	literal := k.stamp + "-session-literal-Zq7"
	k.returned[literal] = "a session's statement"
	channel := "acc-2409-sinks-" + unique1579()
	env := issue2148Channel(t, admin, channel)
	body := quietHealth(channel)
	body["idle_in_transaction_seconds"] = map[string]any{"warning": 5, "critical": 0}
	putSettings(t, admin, healthSettings2406, body)
	_, closeIdle := idleSession2409(t, "orders_app", "orders_secret", "SELECT '"+literal+"'")

	waitFor(t, "the idle session with its statement in the report", func() (bool, string) {
		_, raw := k.rest(admin, http.MethodGet, appdbPath(conn, "/health"), nil)
		return strings.Contains(string(raw), literal), "not yet"
	})
	waitFor(t, "the alert", func() (bool, string) {
		n := issue2148Count(t, env, "WARNING: Application database "+conn+": a session has been idle")
		return n >= 1, fmt.Sprintf("%d posts", n)
	})
	k.rest(admin, http.MethodGet, appdbPath(conn, "/drift"), nil)
	k.rest(admin, http.MethodGet, appdbPath(conn, "/health/history?days=1"), nil)
	k.rest(admin, http.MethodPost, appdbPath(conn, "/maintenance"), map[string]any{"command": "analyze"})
	k.rest(admin, http.MethodPost, appdbPath(conn, "/console"), map[string]any{"sql": "SELECT current_user"})
	closeIdle()

	for _, post := range mattermostPosts(t, env) {
		k.read("mattermost post", []byte(post))
		if strings.Contains(post, literal) {
			t.Errorf("an alert carries the session's statement: %s", post)
		}
	}
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
}

func TestIssue2409_TheAppDBKindRegistersManageSchemaAlone(t *testing.T) {
	appdbToolsUnchanged(t)
}
