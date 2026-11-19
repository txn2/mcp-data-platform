//go:build integration

package acceptance

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// Issue #2717: on PostgreSQL 16 and later the platform drops an application's
// run roles when its connection's user is a CREATEROLE user and not a
// superuser (a superuser hides it): remove_environment leaves no role behind,
// and deleting an application holding a schema succeeds and drops its role.
// The dev stack's target is PostgreSQL 17; the connection is registered for
// the test on a database owned by a user made as such a role.
//
// Wire forms: the connection is registered with PUT
// /api/v1/admin/connection-instances/appdb/{name} and a JSON object {config,
// description}, config an object of strings; manage_application's `command`,
// `application`, `environment`, `kind` and `part_id` are strings, and
// manage_schema's `version` a number, the one form each admits.

// roles2717 is how many roles on the target match a LIKE pattern.
func roles2717(t *testing.T, pattern string) string {
	t.Helper()
	return strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM pg_roles WHERE rolname LIKE '"+pattern+"'"))
}

// TestIssue2717_ACreateRoleConnectionsRunRolesAreDropped: an environment's run
// role is gone after remove_environment, and the application's own after it
// is deleted, which answers without error.
func TestIssue2717_ACreateRoleConnectionsRunRolesAreDropped(t *testing.T) {
	admin := connect(t)
	u := unique1579()
	role, conn, schema, db := "acc2717_owner_"+u, "acc2717-"+u, "acc2717_"+u, "acc2717_db_"+u
	// Cleanups run last first: the connection goes, then the database, then
	// any role left behind by a failure, then the owner.
	dropRolesLater(t, role)
	t.Cleanup(func() {
		targetPSQL(t, "DO $$DECLARE r text; BEGIN FOR r IN SELECT rolname FROM pg_roles WHERE rolname LIKE '"+schema+`\_%' LOOP `+
			"EXECUTE format('DROP ROLE %I', r); END LOOP; END$$")
	})
	targetPSQL(t, "CREATE ROLE "+role+" LOGIN CREATEROLE PASSWORD 'pw-2717'")
	// A database of its own, owned by the connection's user as a deployment's
	// is, so the migration ledger is the user's own.
	targetPSQL(t, "CREATE DATABASE "+db+" OWNER "+role)
	t.Cleanup(func() { targetPSQL(t, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	forgetSchemaLater(t, conn, schema)
	forgetSchemaLater(t, conn, schema+"_dev")

	port := os.Getenv("DEV_PG_TARGET_PORT")
	if port == "" {
		port = "5442"
	}
	status, body := admin.rest(http.MethodPut, "/api/v1/admin/connection-instances/appdb/"+conn, jsonBody(t, map[string]any{
		"config": map[string]any{
			"host": "127.0.0.1", "port": port, "database": db, "user": role, "password": "pw-2717", "sslmode": "disable",
		},
		"description": "Acceptance #2717: a CREATEROLE user that is not a superuser",
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the connection: %d %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/appdb/"+conn, http.NoBody) })

	app := app2539(t, admin, admin, "cr2717")
	addSchema2539(admin, app, conn, schema)
	migration2539(admin, conn, schema, 1, "CREATE TABLE notes (id int)", "DROP TABLE notes")
	admin.application(map[string]any{"command": "release", "application": app, "note": "the table"})
	admin.application(map[string]any{"command": "add_environment", "application": app, "environment": "dev"})
	if n := roles2717(t, schema+`\_dev\_run\_%`); n != "1" {
		t.Fatalf("dev's run role count is %s after add_environment; want 1", n)
	}

	admin.application(map[string]any{"command": "remove_environment", "application": app, "environment": "dev"})
	if n := roles2717(t, schema+`\_dev\_run\_%`); n != "0" {
		t.Errorf("dev's run role is still on the server after remove_environment (%s left)", n)
	}

	if n := roles2717(t, schema+`\_run\_%`); n != "1" {
		t.Fatalf("the application's run role count is %s before the delete; want 1", n)
	}
	res, text, err := admin.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	if err != nil || res.IsError {
		t.Fatalf("deleting the application holding the schema failed: %v %s", err, firstRunes(text, 400))
	}
	if n := roles2717(t, schema+`\_run\_%`); n != "0" {
		t.Errorf("the application's run role is still on the server after the delete (%s left)", n)
	}
}
