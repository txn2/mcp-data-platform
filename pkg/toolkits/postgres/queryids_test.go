package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/sqltag"
)

// TestPlatformStatementsListEveryTaggedStatement holds the list the health
// report's query ids are read from (#2536) to the tag, and to the
// statements built per connection: the ledger read and each drained outbox.
func TestPlatformStatementsListEveryTaggedStatement(t *testing.T) {
	c := &conn{cfg: Config{LedgerSchema: "platform"}}
	c.outboxes.Store(`"shop"."outbox"`, struct{}{})
	stmts := c.platformStatementsOf()
	for _, s := range stmts {
		assert.True(t, strings.HasPrefix(s, sqltag.Platform), s)
	}
	assert.Contains(t, stmts, c.ledgerReadSQL())
	sel, del := outboxSQL(`"shop"."outbox"`)
	assert.Contains(t, stmts, sel)
	assert.Contains(t, stmts, del)
	assert.Contains(t, stmts, claimsSQL, "the per-call claims statement is the platform's own")
	assert.Contains(t, stmts, c.ledgerSchemasSQL())
	assert.Len(t, stmts, len(platformStatements)+4)
}

// TestPlatformQueryIDs reads each statement's id from its EXPLAIN, once per
// distinct id, skipping a statement that does not plan or names no id.
func TestPlatformQueryIDs(t *testing.T) {
	tk := fakeToolkit(baseConfig(), &fakeTx{})
	var asked []string
	tk.connections["db"].explain = func(_ context.Context, sql string) ([]string, error) {
		asked = append(asked, sql)
		switch {
		case strings.Contains(sql, "set_config"):
			return []string{"Result  (cost=0.00..0.01 rows=1 width=128)", "  Output: set_config(...)", "Query Identifier: -5875789104165997195"}, nil
		case strings.Contains(sql, "extra one"):
			return []string{"Query Identifier: 42"}, nil
		case strings.Contains(sql, "extra two"):
			return []string{"Query Identifier: 42"}, nil
		case strings.Contains(sql, "malformed"):
			return []string{"Query Identifier: not-a-number"}, nil
		case strings.Contains(sql, "fails"):
			return nil, errors.New("relation does not exist")
		}
		return []string{"Seq Scan on t"}, nil
	}
	ids, err := tk.PlatformQueryIDs(context.Background(), "db", []string{"SELECT 'extra one'", "SELECT 'extra two'", "SELECT 'malformed'", "SELECT 'fails'"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-5875789104165997195", "42"}, ids)
	for _, sql := range asked {
		assert.True(t, strings.HasPrefix(sql, sqltag.Platform+" EXPLAIN (GENERIC_PLAN, VERBOSE) "), sql)
	}

	_, err = tk.PlatformQueryIDs(context.Background(), "nope", nil)
	require.ErrorIs(t, err, ErrConnectionNotFound)
}

// TestRunRoleName: a schema's run role starts with the schema's name, is
// told apart per database, and stays within PostgreSQL's 63 bytes.
func TestRunRoleNameFor(t *testing.T) {
	a, b := runRoleNameFor("orders", "shop"), runRoleNameFor("ledger", "shop")
	assert.True(t, strings.HasPrefix(a, "shop_run_"))
	assert.NotEqual(t, a, b, "two databases on one server get two roles")
	assert.Equal(t, a, runRoleNameFor("orders", "shop"))
	long := runRoleNameFor("orders", strings.Repeat("s", 63))
	assert.Len(t, long, 63)
	assert.Equal(t, "shop_write", writeRoleName("shop"))
}
