//go:build integration

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Issue #2719: a SECURITY DEFINER function an application's migration pins
// with SET search_path FROM CURRENT, the form the platform tells authors to
// use, is not warned about, and is applied with pg_temp last: the migration
// runs with its schema then pg_temp on the search path, and an environment's
// copy keeps the environment's schema then pg_temp. REVOKE ... FROM PUBLIC is
// accepted, and the environments page says so.
//
// Wire forms: manage_schema's `command`, `connection`, `schema`, `name`, `up`
// and `down` are strings and `version` a number, the one form each admits;
// manage_application's `command`, `application` and `environment` strings;
// fetch's `reference` and `purpose` strings.

const definer2719 = "CREATE FUNCTION invoice_count() RETURNS bigint LANGUAGE sql SECURITY DEFINER " +
	"SET search_path FROM CURRENT AS $$ SELECT count(*) FROM invoices $$; " +
	"REVOKE ALL ON FUNCTION invoice_count() FROM PUBLIC"

// searchPath2719 is the search_path describe reports the function pinned with.
func searchPath2719(t *testing.T, c *client, schema string) string {
	t.Helper()
	out := c.call("manage_schema", map[string]any{"command": "describe", "connection": conn2539, "schema": schema})
	raw := asJSON(out)
	var desc struct {
		Functions []struct {
			Name       string `json:"name"`
			SearchPath string `json:"search_path"`
		} `json:"functions"`
	}
	_ = json.Unmarshal([]byte(raw), &desc)
	for _, f := range desc.Functions {
		if f.Name == "invoice_count" {
			return f.SearchPath
		}
	}
	t.Fatalf("describe of %s holds no invoice_count: %s", schema, firstRunes(raw, 800))
	return ""
}

// TestIssue2719_FromCurrentIsNotWarnedAndPinsPgTempLast: the add answers no
// security_definer_search_path, the applied function pins "<schema>, pg_temp",
// and the dev environment's copy pins "<schema>_dev, pg_temp".
func TestIssue2719_FromCurrentIsNotWarnedAndPinsPgTempLast(t *testing.T) {
	s := newSchemaApp2709(t)
	added := s.admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2539, "schema": s.schema, "version": 2, "name": "definer",
		"up": definer2719, "down": "DROP FUNCTION invoice_count()",
	})
	if rules := findingRules(added); slices.Contains(rules, "security_definer_search_path") || slices.Contains(rules, "role_named") {
		t.Fatalf("adding a FROM CURRENT definer with REVOKE ... FROM PUBLIC reported %v", rules)
	}
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "the definer"})
	if got := searchPath2719(t, s.admin, s.schema); got != s.schema+", pg_temp" {
		t.Errorf("the applied function pins search_path %q; want %q", got, s.schema+", pg_temp")
	}

	s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	if got := searchPath2719(t, s.admin, s.schema+"_dev"); got != s.schema+"_dev, pg_temp" {
		t.Errorf("dev's copy pins search_path %q; want %q", got, s.schema+"_dev, pg_temp")
	}
}

// TestIssue2719_TheEnvironmentsPageSaysRevokeFromPublicIsAccepted: the
// built-in page an agent reads names REVOKE ... FROM PUBLIC as accepted and
// FROM CURRENT as keeping pg_temp last.
func TestIssue2719_TheEnvironmentsPageSaysRevokeFromPublicIsAccepted(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-application-environments",
		"purpose":   "Acceptance #2719: what a migration may name.",
	})
	if err != nil || res.IsError {
		t.Fatalf("fetch the environments page: %v %s", err, firstRunes(text, 400))
	}
	var out struct {
		Document struct {
			Body string `json:"body"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("fetch answered no JSON envelope: %v", err)
	}
	body := strings.Join(strings.Fields(out.Document.Body), " ")
	for _, want := range []string{"`REVOKE ... FROM PUBLIC` is accepted", "`pg_temp` last"} {
		if !strings.Contains(body, want) {
			t.Errorf("the environments page does not say %q", want)
		}
	}
}

// TestIssue2719_FromCurrentAfterTheMigrationMovesThePathIsWarned: a migration
// that sets search_path itself before a FROM CURRENT definer is warned, since
// the function would keep that path and not the schema with pg_temp last.
func TestIssue2719_FromCurrentAfterTheMigrationMovesThePathIsWarned(t *testing.T) {
	s := newSchemaApp2709(t)
	added := s.admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2539, "schema": s.schema, "version": 2, "name": "moved",
		"up": "SET LOCAL search_path TO public; CREATE FUNCTION moved_one() RETURNS int LANGUAGE sql SECURITY DEFINER " +
			"SET search_path FROM CURRENT AS $$ SELECT 1 $$; REVOKE ALL ON FUNCTION moved_one() FROM PUBLIC",
		"down": "DROP FUNCTION moved_one()",
	})
	if rules := findingRules(added); !slices.Contains(rules, "security_definer_search_path") {
		t.Fatalf("a FROM CURRENT definer after the migration set search_path itself reported %v; want security_definer_search_path", rules)
	}
}
