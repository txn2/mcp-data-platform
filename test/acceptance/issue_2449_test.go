//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2449: manage_schema add's review warns on statements that
// leave an application database open and on features the server's major
// lacks, and refuses statements an application's role must not run. Each
// criterion works in a schema of its own on the dev stack's PostgreSQL 17
// `orders` target, with a base migration applied first so the tables the
// rules read are live ones. The rules that need PostgreSQL 18 (an index on a
// virtual generated column, and the version rule's silence on 18) are
// TestReviewSecurityRules_RealDB and TestReviewVersionRulesFollowTheServer_RealDB,
// run in the real-database gate on 17 and 18.
//
// Wire forms: manage_schema takes command, connection, schema, name and up as
// strings and version as an integer; every call sends them as those JSON
// types, and no parameter of the tool admits a second form.

// base2449 is the live tables the rules read: notes has row-level security
// enabled and forced.
const base2449 = `CREATE TABLE notes (id int PRIMARY KEY, owner text NOT NULL, body text);
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE TABLE plain (id int PRIMARY KEY)`

func schemaWithBase2449(t *testing.T, c *client, prefix string) string {
	t.Helper()
	schema := "acc2449_" + prefix + "_" + unique1579()
	schemaCall(c, schema, map[string]any{"command": "add", "version": 1, "name": "base", "up": base2449})
	schemaCall(c, schema, map[string]any{"command": "apply"})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	return schema
}

func TestIssue2449_EachSecurityRuleWarnsOnItsExampleAndNotOnItsSafeForm(t *testing.T) {
	admin := connect(t)
	for _, tc := range []struct{ rule, example, safe string }{
		{
			"security_definer_search_path",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER AS $$SELECT 1$$;\nREVOKE ALL ON FUNCTION f() FROM PUBLIC",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$SELECT 1$$;\nREVOKE ALL ON FUNCTION f() FROM PUBLIC",
		},
		{
			"security_definer_public_execute",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$SELECT 1$$",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$SELECT 1$$;\nREVOKE EXECUTE ON FUNCTION f() FROM PUBLIC",
		},
		{
			"row_security_not_forced",
			"ALTER TABLE plain ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE plain ENABLE ROW LEVEL SECURITY;\nALTER TABLE plain FORCE ROW LEVEL SECURITY",
		},
		{
			"view_bypasses_row_security",
			"CREATE VIEW mine AS SELECT id FROM notes",
			"CREATE VIEW mine WITH (security_invoker = true) AS SELECT id FROM notes",
		},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			schema := schemaWithBase2449(t, admin, strings.ReplaceAll(tc.rule, "_", ""))
			warned := schemaCall(admin, schema, map[string]any{"command": "add", "version": 2, "name": "example", "up": tc.example})
			if rules := findingRules(warned); !has2405(rules, tc.rule) || warned["status"] != "pending" {
				t.Errorf("add of %q reported %v (status %v), want %s and the migration saved", tc.example, rules, warned["status"], tc.rule)
			}
			safe := schemaCall(admin, schema, map[string]any{"command": "add", "version": 2, "name": "safe form", "up": tc.safe})
			if rules := findingRules(safe); len(rules) != 0 {
				t.Errorf("add of the safe form %q reported %v", tc.safe, rules)
			}
		})
	}
}

func TestIssue2449_EachRefusedStatementIsRefusedAtAddNamingIt(t *testing.T) {
	admin := connect(t)
	schema := schemaWithBase2449(t, admin, "refused")
	for _, tc := range []struct{ up, names string }{
		{"COPY notes FROM PROGRAM 'cat /etc/passwd'", "(COPY ... PROGRAM)"},
		{"COPY (SELECT 1) TO PROGRAM 'sh -c id'", "(COPY ... PROGRAM)"},
		{"CREATE LANGUAGE plsample", "(CREATE LANGUAGE)"},
		{"CREATE FUNCTION py() RETURNS int LANGUAGE plpython3u AS $$return 1$$", "in plpython3u"},
		{"CREATE FUNCTION native() RETURNS int AS 'lib', 'f' LANGUAGE C", "in c"},
		{"CREATE EVENT TRIGGER audit ON ddl_command_end EXECUTE FUNCTION pg_catalog.now()", "(CREATE EVENT TRIGGER)"},
		{"ALTER SYSTEM SET work_mem = '1GB'", "(ALTER SYSTEM)"},
	} {
		text := schemaRefused(t, admin, schema, map[string]any{"command": "add", "version": 2, "name": "refused", "up": "SELECT 1;\n" + tc.up})
		if !strings.Contains(text, "migration 2 is refused") || !strings.Contains(text, "statement 2 ") || !strings.Contains(text, tc.names) {
			t.Errorf("add of %q answered %s, want a refusal naming statement 2 %s", tc.up, text, tc.names)
		}
	}
	status := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Errorf("a refused migration was saved: %v", status)
	}
}

func TestIssue2449_AnEighteenFeatureIsNamedAgainstTheSeventeenTarget(t *testing.T) {
	admin := connect(t)
	schema := schemaWithBase2449(t, admin, "version")
	text := schemaRefused(t, admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "ids", "up": "CREATE TABLE ids (id uuid PRIMARY KEY DEFAULT uuidv7())",
	})
	if !strings.Contains(text, "uses uuidv7(), which PostgreSQL 18 introduced; the connection's server is PostgreSQL 17") ||
		!strings.Contains(text, "gen_random_uuid()") {
		t.Errorf("the add answered %s, want the feature, the server's major and the form 17 has", text)
	}
	safe := schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "ids", "up": "CREATE TABLE ids (id uuid PRIMARY KEY DEFAULT gen_random_uuid())",
	})
	if rules := findingRules(safe); len(rules) != 0 || safe["status"] != "pending" {
		t.Errorf("the 17 form reported %v (status %v)", rules, safe["status"])
	}
}
