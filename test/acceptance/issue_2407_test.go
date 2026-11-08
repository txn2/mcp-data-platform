//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2407: a script's tests run against a throwaway database
// built from the connection's migrations. Each criterion keeps its migrations
// in a schema of its own on the dev stack's `orders` application database,
// whose user creates databases on postgres-target.
//
// Wire forms: manage_schema takes command, connection, schema, name and up as
// strings and version and to_version as integers; manage_script takes
// command, name and source as strings; the admin connection route takes a
// JSON object of config strings and integers. Every call below sends those
// JSON types, and no parameter admits a second form.

// users2407 adds the users table and, at version 2, the unique email.
func users2407(t *testing.T, c *client) string {
	t.Helper()
	schema := "acc2407_" + unique1579()
	holdSchema(t, c, conn2404, schema)
	schemaCall(c, schema, map[string]any{
		"command": "add", "version": 1, "name": "users",
		"up": "CREATE TABLE users (id bigserial PRIMARY KEY, email text NOT NULL)",
	})
	schemaCall(c, schema, map[string]any{"command": "apply", "to_version": 1})
	schemaCall(c, schema, map[string]any{
		"command": "add", "version": 2, "name": "unique email",
		"up": "ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email)",
	})
	return schema
}

// usersScript2407 adds one user, and tests that a second with the same email
// is refused, against a database built from schema's migrations.
func usersScript2407(conn, schema string) string {
	insert := `platform.execute("INSERT INTO ` + schema + `.users (email) VALUES (:e)", connection="` + conn + `", params={"e": "first@example.com"})`
	return `def main():
    """Adds the first user."""
    ` + insert + `

def test_a_duplicate_email_is_refused():
    testing.database("` + conn + `", schemas=["` + schema + `"])
    main()
    msg = assert.fails(lambda: ` + insert + `)
    assert.contains(msg, "23505")
`
}

func scriptTest2407(t *testing.T, c *client, name, source string) map[string]any {
	t.Helper()
	return c.call("manage_script", map[string]any{"command": "test", "name": name, "source": source})
}

func TestIssue2407_ADuplicateEmailTestPassesAgainstTheCloneAndFailsWithoutTheConstraint(t *testing.T) {
	admin := connect(t)
	schema := users2407(t, admin)
	name := "acceptance-2407-unique-" + unique1579()
	source := usersScript2407(conn2404, schema)

	out := scriptTest2407(t, admin, name, source)
	if out["ok"] != true {
		t.Fatalf("the test did not pass against a database built from the migrations: %v", out)
	}

	// The constraint's migration is still pending, so it can be replaced:
	// the schema's migrations no longer add the constraint.
	schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "no constraint",
		"up": "COMMENT ON TABLE users IS 'emails are not unique'",
	})
	out = scriptTest2407(t, admin, name, source)
	failure, _ := firstTest1939(t, out)["failure"].(string)
	if out["ok"] != false || !strings.Contains(failure, "did not fail") {
		t.Fatalf("with the constraint's migration removed the test must fail at assert.fails: %v", out)
	}
}

func TestIssue2407_ATestsWritesAreAbsentFromTheConnectionsDatabase(t *testing.T) {
	admin := connect(t)
	schema := users2407(t, admin)
	out := scriptTest2407(t, admin, "acceptance-2407-writes-"+unique1579(), usersScript2407(conn2404, schema))
	if out["ok"] != true {
		t.Fatalf("the test did not pass: %v", out)
	}
	if n := sqlCount(t, admin, "SELECT count(*) AS n FROM "+schema+".users"); n != "0" {
		t.Errorf("the connection's own users table holds %s rows after the test; want none", n)
	}
	// Nothing the test made is left on the server either.
	left := targetPSQL(t, "SELECT count(*) FROM pg_database WHERE datname LIKE 'mcpc\\_%'")
	if strings.TrimSpace(left) != "0" {
		t.Errorf("%s clones are left on the server after the test", strings.TrimSpace(left))
	}
}

func TestIssue2407_AKilledTestRunsCloneIsGoneAfterTheNextSweep(t *testing.T) {
	// What a run killed mid-test leaves: a clone it never dropped, made two
	// hours ago, as the name records.
	name := fmt.Sprintf("mcpc_%d_%016x", time.Now().Add(-2*time.Hour).Unix(), time.Now().UnixNano())
	targetPSQL(t, "CREATE DATABASE "+name)
	deadline := time.Now().Add(time.Minute)
	for {
		n := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM pg_database WHERE datname = '"+name+"'"))
		if n == "0" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the leftover clone %s was still on the server a minute later; the sweep runs every MCP_PLATFORM_APPDB_TEST_SWEEP_INTERVAL", name)
		}
		time.Sleep(time.Second)
	}
}

func TestIssue2407_AConnectionWhoseUserCannotCreateDatabasesSaysSoAtTestTime(t *testing.T) {
	admin := connect(t)
	role := "acc2407_nocreate_" + unique1579()
	made := draft2428(admin, "acceptance-2407-role", `
def main():
    """Makes a role that cannot create databases."""
    platform.execute("CREATE ROLE `+role+` LOGIN PASSWORD 'acceptance' NOCREATEDB", connection="`+conn2404+`")
`, true)
	if made["status"] != "succeeded" {
		t.Fatalf("creating the role failed: %v", made)
	}
	port := os.Getenv("DEV_PG_TARGET_PORT")
	if port == "" {
		port = "5442"
	}
	conn := "acc-2407-nocreate-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{"host": "localhost", "port": port, "database": "orders", "user": role, "password": "acceptance", "sslmode": "disable"}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2407"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, path, http.NoBody)
		targetPSQL(t, "DROP ROLE IF EXISTS "+role)
	})

	out := scriptTest2407(t, admin, "acceptance-2407-nocreate-"+unique1579(), `def main():
    """Counts nothing."""
    return None

def test_needs_a_database():
    testing.database("`+conn+`")
    assert.eq(platform.query("SELECT 1 AS n", connection="`+conn+`")["rows"][0]["n"], 1)
`)
	failure, _ := firstTest1939(t, out)["failure"].(string)
	if out["ok"] != false || !strings.Contains(failure, "CREATEDB") || !strings.Contains(failure, role) {
		t.Fatalf("the test must fail naming the CREATEDB privilege and the user: %v", out)
	}
}

// targetPSQL runs one statement on the dev stack's postgres-target as its
// owner and returns what psql printed, unaligned and without headers.
func targetPSQL(t *testing.T, statement string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", "acme-dev-postgres-target", // #nosec G204 -- the dev stack's own container
		"psql", "-v", "ON_ERROR_STOP=1", "-At", "-U", "orders_app", "-d", "orders", "-c", statement).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v: %s", statement, err, out)
	}
	return string(out)
}
