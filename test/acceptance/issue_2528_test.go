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

	_ "github.com/lib/pq" // the criteria run statements on the target as the application and as another role
)

// Acceptance for #2528: on an application database with pg_stat_statements
// installed, after the health route has been called twice, none of the
// statements it ranks is one of the report's own or the toolkit's
// bookkeeping; a statement another role ran, whose text the connection's role
// cannot read, is counted in hidden_other_roles and not listed; and a session
// held idle in a transaction past the threshold comes back as a warning
// finding and sets the status to warning.
//
// Wire forms: the health route takes the connection in its path and no body;
// the settings route takes one JSON object of numbers, a list of strings and
// threshold objects. No parameter admits a second form.

// db2528 is a fresh database on the target with pg_stat_statements
// installed, owned by a role that is neither a superuser nor a reader of
// every role's statistics, and an appdb connection logging in as it.
type db2528 struct {
	name, role, password, conn string
}

func newDB2528(t *testing.T, admin *client) db2528 {
	t.Helper()
	stamp := unique1579()
	d := db2528{name: "acc2528_" + stamp, role: "acc2528_app_" + stamp, password: "acc2528-" + stamp}
	targetPSQL(t, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", d.role, d.password))
	targetPSQL(t, fmt.Sprintf("CREATE ROLE %s_other LOGIN PASSWORD '%s'", d.role, d.password))
	targetPSQL(t, "CREATE DATABASE "+d.name+" OWNER "+d.role)
	t.Cleanup(func() {
		targetPSQL(t, "DROP DATABASE IF EXISTS "+d.name+" WITH (FORCE)")
		targetPSQL(t, "DROP ROLE IF EXISTS "+d.role+"_other")
		targetPSQL(t, "DROP ROLE IF EXISTS "+d.role)
	})
	d.exec(t, "orders_app", "orders_secret", "CREATE EXTENSION pg_stat_statements")
	d.exec(t, d.role, d.password, "CREATE TABLE items (id bigint PRIMARY KEY, sku text NOT NULL)")
	d.exec(t, d.role, d.password, "GRANT SELECT ON items TO "+d.role+"_other")

	d.conn = "acc-2528-" + stamp
	path := "/api/v1/admin/connection-instances/appdb/" + d.conn
	cfg := map[string]any{
		"host": "localhost", "port": targetPort(), "database": d.name, "user": d.role, "password": d.password,
		"sslmode": "disable", "max_conns": 4,
	}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2528"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving connection %s answered %d: %v", d.conn, status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	return d
}

// open connects to the database as user.
func (d db2528) open(t *testing.T, user, password string) *sql.DB {
	t.Helper()
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(user, password), Host: "localhost:" + targetPort(),
		Path: "/" + d.name, RawQuery: "sslmode=disable&application_name=acc-2528",
	}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// exec runs one statement on the database as user.
func (d db2528) exec(t *testing.T, user, password, statement string) {
	t.Helper()
	if _, err := d.open(t, user, password).Exec(statement); err != nil {
		t.Fatalf("%s as %s: %v", statement, user, err)
	}
}

func TestIssue2528_StatementsAreTheApplicationsAndUnreadableOnesAreCounted(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	d := newDB2528(t, admin)
	if !strings.Contains(targetPSQL(t, "SHOW shared_preload_libraries"), "pg_stat_statements") {
		t.Fatal("the dev stack's target does not load pg_stat_statements; recreate it from dev/docker-compose.yml")
	}
	// The application's own statement, and another role's.
	d.exec(t, d.role, d.password, "SELECT count(*) FROM items WHERE sku = 'acc-2528-app'")
	d.exec(t, d.role+"_other", d.password, "SELECT count(*) FROM items WHERE sku = 'acc-2528-other'")

	health(t, admin, d.conn)
	report := health(t, admin, d.conn)
	statements, _ := report["statements"].(map[string]any)
	if statements["installed"] != true {
		t.Fatalf("the report does not read pg_stat_statements: %v", statements)
	}
	top := asList(statements["top"])
	sawApp := false
	for _, s := range top {
		q := fmt.Sprint(s["query"])
		if strings.Contains(q, "/* mcp-data-platform */") || strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "SET LOCAL") ||
			strings.HasPrefix(q, "DISCARD") || q == "<insufficient privilege>" {
			t.Errorf("the ranking lists a statement that is not the application's: %q", q)
		}
		if strings.Contains(q, "FROM items WHERE sku") {
			sawApp = true
		}
		if _, ok := s["max_ms"]; !ok {
			t.Errorf("a statement carries no slowest call: %v", s)
		}
	}
	if !sawApp {
		t.Errorf("the application's own statement is not ranked: %v", top)
	}
	if n, _ := statements["hidden_other_roles"].(float64); n < 1 {
		t.Errorf("the other role's statement is not counted hidden: %v", statements["hidden_other_roles"])
	}
}

func TestIssue2528_AnIdleSessionPastTheThresholdIsAWarning(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	d := newDB2528(t, admin)
	body := quietHealth("acc-2528-" + unique1579())
	body["idle_in_transaction_seconds"] = map[string]any{"warning": 3, "critical": 0}
	putSettings(t, admin, healthSettings2406, body)

	if report := health(t, admin, d.conn); report["status"] != "healthy" {
		t.Errorf("with nothing past a threshold the status reads %v: %v", report["status"], report["findings"])
	}
	db := d.open(t, d.role, d.password)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var pid float64
	if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the idle session as a warning finding", func() (bool, string) {
		report := health(t, admin, d.conn)
		for _, f := range asList(report["findings"]) {
			if f["kind"] == "idle_in_transaction" && f["pid"] == pid {
				return report["status"] == "warning" && f["level"] == "warning", fmt.Sprintf("status %v, finding %v", report["status"], f)
			}
		}
		return false, fmt.Sprintf("status %v, findings %v", report["status"], report["findings"])
	})
}
