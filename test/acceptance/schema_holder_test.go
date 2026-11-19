//go:build integration

package acceptance

import (
	"os/exec"
	"testing"
)

// holdSchema puts schema on conn in an application of its own, so that
// manage_schema adds, plans and applies its migrations: a schema no
// application holds is refused for migrations (#2539). The application, and
// the run role adding the schema created, go when the test ends; the schema
// stays for the test's own cleanup.
func holdSchema(t *testing.T, c *client, conn, schema string) string {
	t.Helper()
	name := "acc-hold-" + unique1579()
	forgetSchemaLater(t, conn, schema)
	c.call("manage_application", map[string]any{
		"command": "create", "name": name, "description": "Acceptance: holds schema " + schema + " for its migrations",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	c.call("manage_application", map[string]any{"command": "add_part", "application": name, "kind": "schema", "part_id": conn + "/" + schema})
	return name
}

// writeRole is what a migration adds to reach its tables from the
// application's scripts: nothing since #2709, which grants each
// environment's run role DML on what its schema's migrations create; a
// migration naming a role is refused. Before #2709 it defined
// <schema>_write and granted it privileges on tables.
func writeRole(_, _, _ string) string { return "SELECT 1" }

// dropRolesLater drops roles on the dev stack's target server when the test
// ends, best effort: a role is the server's, and a schema's cleanup leaves it.
func dropRolesLater(t *testing.T, roles ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, r := range roles {
			_ = exec.Command("docker", "exec", "acme-dev-postgres-target", // #nosec G204 -- the dev stack's own container
				"psql", "-At", "-U", "orders_app", "-d", "orders", "-c",
				"DO $$BEGIN EXECUTE format('DROP OWNED BY %I', '"+r+"'); EXECUTE format('DROP ROLE %I', '"+r+"'); "+
					"EXCEPTION WHEN OTHERS THEN NULL; END$$").Run()
		}
	})
}

// forgetSchemaLater removes, when the test ends, the migrations the platform
// keeps for schema on conn and the target ledger's rows of it. A test that
// drops its schema and keeps its migrations leaves a schema the health loop
// reads as drifted on every pass, and those alerts flood whatever channel the
// next test points the alerts at.
func forgetSchemaLater(t *testing.T, conn, schema string) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec.Command("docker", "exec", "acme-dev-postgres", // #nosec G204 -- the dev stack's own container
			"psql", "-At", "-U", "platform", "-d", "mcp_platform", "-c",
			"DELETE FROM appdb_migrations WHERE connection = '"+conn+"' AND schema_name = '"+schema+"'").Run()
		_ = exec.Command("docker", "exec", "acme-dev-postgres-target", // #nosec G204 -- the dev stack's own container
			"psql", "-At", "-U", "orders_app", "-d", "orders", "-c",
			"DO $$BEGIN IF to_regclass('mcp_schema.migrations') IS NOT NULL THEN DELETE FROM mcp_schema.migrations WHERE schema_name = '"+schema+"'; END IF; END$$").Run()
	})
}
