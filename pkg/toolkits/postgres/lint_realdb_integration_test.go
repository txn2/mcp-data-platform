//go:build integration

package postgres

import (
	"strings"
	"testing"
)

// findingRules is the rules of an add's findings.
func findingRules(out map[string]any) []string {
	var rules []string
	fs, _ := out["findings"].([]any)
	for _, f := range fs {
		m, _ := f.(map[string]any)
		r, _ := m["rule"].(string)
		rules = append(rules, r)
	}
	return rules
}

// The review reads the server's major from the connection at add (#2449): a
// PostgreSQL 18 feature is named in the refusal on 17, whose from-zero apply
// fails on it, and passes silently on 18.
func TestReviewVersionRulesFollowTheServer_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newSchemaTarget(t, nil)
		ctx := admin()
		in := schemaInput{Command: schemaAdd, Version: 1, Name: "ids", Up: "CREATE TABLE ids (id uuid PRIMARY KEY DEFAULT uuidv7())"}
		if subtestMajor(t.Name()) == "pg17" {
			out := schema(t, ctx, tk, in, true)
			if msg, _ := out["error"].(string); !strings.Contains(msg, "uses uuidv7(), which PostgreSQL 18 introduced; the connection's server is PostgreSQL 17") {
				t.Errorf("on 17 the refusal must name the feature: %v", out)
			}
			return
		}
		if rules := findingRules(schema(t, ctx, tk, in, false)); len(rules) != 0 {
			t.Errorf("on 18 uuidv7() is the server's own: %v", rules)
		}
	})
}

// The security rules read the schema's row-level security from the server:
// a view over a table secured by an applied migration is flagged in a later
// one, and a refused statement never saves.
func TestReviewSecurityRules_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, store := newSchemaTarget(t, nil)
		ctx := admin()
		added := schema(t, ctx, tk, schemaInput{
			Command: schemaAdd, Version: 1, Name: "notes",
			Up: "CREATE TABLE notes (id int PRIMARY KEY, owner text NOT NULL);\nALTER TABLE notes ENABLE ROW LEVEL SECURITY",
		}, false)
		if rules := findingRules(added); len(rules) != 1 || rules[0] != "row_security_not_forced" {
			t.Errorf("enabling row security without forcing it: %v", rules)
		}
		schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)

		view := schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "mine", Up: "CREATE VIEW mine AS SELECT id FROM notes"}, false)
		if rules := findingRules(view); len(rules) != 1 || rules[0] != "view_bypasses_row_security" {
			t.Errorf("a view over the applied table with row security: %v", rules)
		}
		safe := schema(t, ctx, tk, schemaInput{
			Command: schemaAdd, Version: 2, Name: "mine",
			Up: "CREATE VIEW mine WITH (security_invoker = true) AS SELECT id FROM notes",
		}, false)
		if rules := findingRules(safe); len(rules) != 0 {
			t.Errorf("the security_invoker view: %v", rules)
		}

		if subtestMajor(t.Name()) != "pg17" {
			virtual := schema(t, ctx, tk, schemaInput{
				Command: schemaAdd, Version: 2, Name: "mine",
				Up: "CREATE TABLE totals (a int, b int GENERATED ALWAYS AS (a * 2));\nCREATE INDEX totals_b ON totals (b)",
			}, true)
			if msg, _ := virtual["error"].(string); !strings.Contains(msg, "indexes totals.b, a generated column declared without STORED") {
				t.Errorf("an index on a virtual generated column fails from zero on 18, and the refusal must say why: %v", virtual)
			}
		}

		held := len(store.migrations)
		refused := schema(t, ctx, tk, schemaInput{
			Command: schemaAdd, Version: 3, Name: "copy",
			Up: "COPY notes FROM PROGRAM 'cat /etc/passwd'",
		}, true)
		if msg, _ := refused["error"].(string); !strings.Contains(msg, "statement 1 (COPY ... PROGRAM)") || len(store.migrations) != held {
			t.Errorf("COPY FROM PROGRAM must be refused and not saved: %v", refused)
		}
	})
}
