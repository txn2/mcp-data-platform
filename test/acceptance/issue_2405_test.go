//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Acceptance for #2405: manage_schema add reviews a migration before it
// saves. Each criterion works in a schema of its own on the dev stack's
// `orders` application database, with a base migration applied first so the
// tables the rules read are live ones.
//
// Wire forms: manage_schema takes command, connection, schema, name, up and
// change_summary as strings, version as an integer and transactional and
// user_agreed as booleans; every call below sends them as those JSON types,
// and no parameter of the tool admits a second form. manage_script takes
// command, name and source as strings.

// base2405 is the live tables every rule reads.
const base2405 = `CREATE TABLE orders (id bigint PRIMARY KEY, email text, total numeric(10,2), note text, customer_id bigint);
CREATE TABLE customers (id bigint PRIMARY KEY)`

// schemaWithBase2405 is a fresh schema with the base migration applied.
func schemaWithBase2405(t *testing.T, c *client, prefix string) string {
	t.Helper()
	schema := "acc2405_" + prefix + "_" + unique1579()
	holdSchema(t, c, conn2404, schema)
	schemaCall(c, schema, map[string]any{"command": "add", "version": 1, "name": "base", "up": base2405})
	schemaCall(c, schema, map[string]any{"command": "apply"})
	return schema
}

// findingRules is the rules an add answer reports.
func findingRules(out map[string]any) []string {
	var rules []string
	findings, _ := out["findings"].([]any)
	for _, raw := range findings {
		if f, ok := raw.(map[string]any); ok {
			rules = append(rules, f["rule"].(string))
		}
	}
	return rules
}

func has2405(rules []string, rule string) bool {
	for _, r := range rules {
		if r == rule {
			return true
		}
	}
	return false
}

func TestIssue2405_EachLockingRuleWarnsOnItsExampleAndPassesOnItsSafeForm(t *testing.T) {
	admin := connect(t)
	for _, tc := range []struct {
		rule, example, safe string
		safeTransactional   bool
	}{
		{"create_index_locks", "CREATE INDEX orders_email ON orders (email)", "CREATE INDEX CONCURRENTLY orders_email ON orders (email)", false},
		{"not_null_without_default", "ALTER TABLE orders ADD COLUMN region text NOT NULL", "ALTER TABLE orders ADD COLUMN region text NOT NULL DEFAULT 'us'", true},
		{"volatile_default_rewrites", "ALTER TABLE orders ADD COLUMN token uuid DEFAULT gen_random_uuid()", "ALTER TABLE orders ADD COLUMN token uuid", true},
		{"type_change_rewrites", "ALTER TABLE orders ALTER COLUMN total TYPE numeric(14,2)", "ALTER TABLE orders ADD COLUMN total_v2 numeric(14,2)", true},
		{
			"constraint_validates", "ALTER TABLE orders ADD CONSTRAINT orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)",
			"ALTER TABLE orders ADD CONSTRAINT orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id) NOT VALID", true,
		},
		{
			"lock_timeout_raised", "SET LOCAL lock_timeout = '1min';\nALTER TABLE orders ADD COLUMN memo text",
			"SET LOCAL lock_timeout = '5s';\nALTER TABLE orders ADD COLUMN memo text", true,
		},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			schema := schemaWithBase2405(t, admin, strings.ReplaceAll(tc.rule, "_", ""))
			warned := schemaCall(admin, schema, map[string]any{"command": "add", "version": 2, "name": "example", "up": tc.example})
			if rules := findingRules(warned); !has2405(rules, tc.rule) {
				t.Errorf("add of %q reported %v, want %s", tc.example, rules, tc.rule)
			}
			if warned["status"] != "pending" {
				t.Errorf("a warning kept the migration from saving: %v", warned)
			}
			safe := schemaCall(admin, schema, map[string]any{
				"command": "add", "version": 2, "name": "safe form",
				"up": tc.safe, "transactional": tc.safeTransactional,
			})
			if rules := findingRules(safe); has2405(rules, tc.rule) {
				t.Errorf("add of the safe form %q still reported %s", tc.safe, tc.rule)
			}
			if safe["replaced"] != true {
				t.Errorf("the safe form did not replace the pending example: %v", safe)
			}
		})
	}
}

func TestIssue2405_EachDestructiveRuleRefusesItsExampleAndPassesOnItsSafeForm(t *testing.T) {
	admin := connect(t)
	for _, tc := range []struct {
		rule, example, safe string
		agreed              bool
	}{
		{"drop_table", "DROP TABLE customers", "DROP TABLE customers", true},
		{"truncate", "TRUNCATE orders", "TRUNCATE orders", true},
		{"unbounded_delete", "DELETE FROM orders", "DELETE FROM orders WHERE total IS NULL", false},
		{"unbounded_update", "UPDATE orders SET note = ''", "UPDATE orders SET note = '' WHERE note IS NULL", false},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			schema := schemaWithBase2405(t, admin, strings.ReplaceAll(tc.rule, "_", ""))
			text := schemaRefused(t, admin, schema, map[string]any{"command": "add", "version": 2, "name": "example", "up": tc.example})
			if !strings.Contains(text, "removes data") || !strings.Contains(text, "change_summary") {
				t.Errorf("add of %q without a summary: %s", tc.example, text)
			}
			args := map[string]any{"command": "add", "version": 2, "name": "safe form", "up": tc.safe}
			if tc.agreed {
				args["change_summary"], args["user_agreed"] = "Removes what "+tc.safe+" removes, as agreed.", true
			}
			out := schemaCall(admin, schema, args)
			if out["status"] != "pending" {
				t.Errorf("the safe form did not save: %v", out)
			}
		})
	}
}

func TestIssue2405_ADropColumnSavesOnlyWithItsSummaryAndTheSummaryIsInItsHistory(t *testing.T) {
	admin := connect(t)
	schema := schemaWithBase2405(t, admin, "dropcol")
	drop := map[string]any{"command": "add", "version": 2, "name": "drop note", "up": "ALTER TABLE orders DROP COLUMN note"}
	text := schemaRefused(t, admin, schema, drop)
	if !strings.Contains(text, "drops column note of orders") {
		t.Errorf("the refusal does not name what it removes: %s", text)
	}
	status := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Fatalf("the refused migration was saved: %v", status)
	}

	summary := "Removes the free-text note from every order; nothing reads it since the notes moved to tickets."
	drop["change_summary"], drop["user_agreed"] = summary, true
	out := schemaCall(admin, schema, drop)
	if out["status"] != "pending" || !has2405(findingRules(out), "drop_column") {
		t.Fatalf("add with the summary: %v", out)
	}
	history := schemaCall(admin, schema, map[string]any{"command": "history", "limit": 10})
	raw, _ := json.Marshal(history["events"])
	if !strings.Contains(string(raw), summary) || !strings.Contains(string(raw), "agreed by") {
		t.Errorf("the history does not hold the agreed summary: %s", raw)
	}
}

func TestIssue2405_ARenameOfAColumnASavedScriptQueriesNamesThatScript(t *testing.T) {
	admin := connect(t)
	schema := schemaWithBase2405(t, admin, "rename")
	name := "acceptance-2405-reader-" + unique1579()
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": `
def main():
    """Reads each order's contact address."""
    rows = platform.query("SELECT id, email FROM ` + schema + `.orders ORDER BY id", connection="` + conn2404 + `")["rows"]
    print(len(rows))
`}, nil)
	out := schemaCall(admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "rename email",
		"up": "ALTER TABLE orders RENAME COLUMN email TO contact_email",
	})
	findings, _ := out["findings"].([]any)
	var named []any
	for _, raw := range findings {
		if f, _ := raw.(map[string]any); f["rule"] == "rename_referenced" {
			named, _ = f["scripts"].([]any)
		}
	}
	found := false
	for _, s := range named {
		found = found || s == name
	}
	if !found {
		t.Errorf("the rename's findings do not name %s: %v", name, findings)
	}
}

func TestIssue2405_AMigrationThatDoesNotApplyFromZeroIsRefused(t *testing.T) {
	admin := connect(t)
	schema := schemaWithBase2405(t, admin, "fromzero")
	text := schemaRefused(t, admin, schema, map[string]any{
		"command": "add", "version": 2, "name": "wrong table",
		"up": "ALTER TABLE shipments ADD COLUMN carrier text",
	})
	if !strings.Contains(text, "do not apply from zero") || !strings.Contains(text, "migration 2") {
		t.Errorf("a migration naming a table no migration creates: %s", text)
	}
}
