package postgres

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pglex"
)

// splitStatements cuts SQL into its top-level statements (pglex.Split),
// dropping one that holds no keyword (only a comment).
func splitStatements(sql string) []string {
	var out []string
	for _, s := range pglex.Split(sql) {
		if firstWord(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// checkMigrationSQL refuses migration SQL that would end the transaction a
// migration runs in -- a top-level COMMIT would commit the statements before
// it, run the ones after it outside any transaction, and let a plan, which
// rolls back, keep them -- and a migration that runs outside a transaction
// holding more than its one statement.
func checkMigrationSQL(sql string, transactional bool) error {
	stmts := splitStatements(sql)
	for i, s := range stmts {
		if word := firstWord(s); transactionControl[word] {
			return fmt.Errorf("statement %d is %s, and a migration does not end or split the transaction it runs in; "+
				"the platform commits each migration itself", i+1, strings.ToUpper(word))
		}
	}
	if !transactional && len(stmts) != 1 {
		return fmt.Errorf("a migration that runs outside a transaction holds exactly one statement (it holds %d); "+
			"give each such statement a migration of its own", len(stmts))
	}
	return nil
}
