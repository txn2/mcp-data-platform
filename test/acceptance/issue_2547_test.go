//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2547: a FROM inside a function call is not a table.
//
// Wire forms: manage_application takes command, application, kind and part_id
// as strings; manage_schema takes command, connection, schema, name and up as
// strings and version as an integer; manage_script takes command, name and
// source as strings. The application is read through manage_application get
// and through GET /api/v1/portal/applications/{name}.
func TestIssue2547_AnExtractColumnIsNotReportedAsAForeignTable(t *testing.T) {
	admin := connect(t)
	schema := "acc2547_" + unique1579()
	app := holdSchema(t, admin, conn2402, schema)
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": conn2402, "schema": schema, "version": 1, "name": "samples",
		"up": "CREATE TABLE samples (client text NOT NULL, ts timestamptz NOT NULL, label text NOT NULL)",
	})
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2402, "schema": schema})

	name := "acc-2547-" + unique1579()
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": `
def main():
    """Buckets the samples."""
    platform.result(platform.query("SELECT client, ceil(extract(epoch FROM ts) / 7200) AS bucket, substring(label FROM 1 FOR 2) AS s, trim(BOTH FROM label) AS t FROM ` + schema + `.samples", connection="` + conn2402 + `"))
`}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	addScriptPart2517(t, admin, app, name)

	got := asJSON(admin.call("manage_application", map[string]any{"command": "get", "application": app}))
	for _, column := range []string{"uses ts,", "uses label,", "uses 1,"} {
		if strings.Contains(got, column) {
			t.Errorf("manage_application get reports a column as a table (%q): %s", column, got)
		}
	}
	status, body := admin.rest("GET", "/api/v1/portal/applications/"+app, nil)
	if status != 200 {
		t.Fatalf("GET the application: %d %v", status, body)
	}
	if text := asJSON(body); strings.Contains(text, "uses ts,") || strings.Contains(text, "foreign_schema") {
		t.Errorf("the portal route reports a false finding: %s", text)
	}
}
