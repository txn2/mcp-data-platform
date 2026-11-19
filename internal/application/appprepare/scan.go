// Package appprepare prepares an application written for one schema to run
// in environments of their own (#2709). An application's environments each
// run in a schema of their own, with the search path set to it, so SQL that
// names the application's schema, and migrations that name its roles, would
// reach another environment's data from whichever environment runs them.
// While anything in the application does, a new environment is refused, and
// prepare proposes the same text with the schema qualifier and the role
// statements removed. In the application's current schema the search path is
// already that schema, so the change does nothing there; the save gate
// replays each changed script's recent recorded runs to show the answers are
// the same, and a difference blocks the save.
package appprepare

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pglex"
	"github.com/txn2/mcp-data-platform/internal/pglint"
	"github.com/txn2/mcp-data-platform/internal/schemaref"
)

// migrationText is a migration's SQL with every statement that names a role
// left out and the schema qualifier removed from the rest, and how many
// names that rewrote or left out. It reads statements as the migration lint
// does, so what prepare removes is what a new migration is refused for.
func migrationText(sql, schema string) (out string, names int) {
	if strings.TrimSpace(sql) == "" {
		return sql, 0
	}
	kept := []string{}
	for _, stmt := range pglex.Split(sql) {
		findings, _ := pglint.Lint(stmt, pglint.Target{Schema: schema})
		roles := 0
		for _, f := range findings {
			if f.Rule == pglint.RuleRoleNamed {
				roles++
			}
		}
		if roles > 0 {
			names += roles
			continue
		}
		stmt, k := schemaref.Unqualified(stmt, []string{schema})
		names += k
		kept = append(kept, stmt)
	}
	if names == 0 {
		return sql, 0
	}
	if len(kept) == 0 {
		return keptNothing, names
	}
	return strings.Join(kept, ";\n") + ";\n", names
}

// keptNothing is a migration every statement of which named a role: it
// still applies, doing nothing, so the versions after it keep their order.
const keptNothing = "-- Every statement named a role; prepare removed them (#2709).\nSELECT 1;\n"
