//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2720: manage_schema status on an application environment's schema
// reads the application's migrations, which are kept under its schema and
// applied to each environment's: no drift, the schema compared with what the
// applied migrations build, and the same answer the application's own schema
// gets.
//
// Wire forms: manage_schema's `command`, `connection` and `schema` are
// strings, the one form each admits; manage_application's `command`,
// `application` and `environment` strings.

// TestIssue2720_AnEnvironmentsSchemaReportsNoDrift: after add_environment dev,
// status on <schema>_dev reports no drift, version 1 applied, the schema
// checked with no differences, and never the advice to adopt it.
func TestIssue2720_AnEnvironmentsSchemaReportsNoDrift(t *testing.T) {
	s := newSchemaApp2709(t)
	s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})

	for _, schema := range []string{s.schema, s.schema + "_dev"} {
		st := s.admin.call("manage_schema", map[string]any{"command": "status", "connection": conn2539, "schema": schema})
		raw := asJSON(st)
		if drift, _ := st["drift"].([]any); len(drift) != 0 {
			t.Errorf("status on %s reports drift %v", schema, drift)
		}
		if strings.Contains(raw, "holds no such migration") || strings.Contains(raw, "adopt it") {
			t.Errorf("status on %s reads the schema as unmanaged: %s", schema, firstRunes(raw, 600))
		}
		applied, _ := st["applied"].([]any)
		pending, _ := st["pending"].([]any)
		if len(applied) != 1 || len(pending) != 0 {
			t.Errorf("status on %s lists %d applied and %d pending; want the application's one applied: %s", schema, len(applied), len(pending), firstRunes(raw, 600))
		}
		if st["application"] != s.name {
			t.Errorf("status on %s names application %v; want %s", schema, st["application"], s.name)
		}
		sd := asMap(st["schema_drift"])
		if sd["checked"] != true {
			t.Errorf("status on %s did not compare the schema: %v", schema, sd)
		}
		if diffs, _ := sd["differences"].([]any); len(diffs) != 0 {
			t.Errorf("status on %s finds differences %v", schema, diffs)
		}
	}
}
