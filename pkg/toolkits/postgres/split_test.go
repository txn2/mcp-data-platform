package postgres

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitStatements(t *testing.T) {
	sql := `CREATE TABLE a (s text DEFAULT 'x;y', "we;ird" int); -- a comment; COMMIT
CREATE FUNCTION f() RETURNS void AS $body$ BEGIN PERFORM 1; COMMIT; END $body$ LANGUAGE plpgsql;
/* nested /* COMMIT; */ still a comment; */ INSERT INTO a VALUES (E'it\'s; fine', 1);
DO $$ BEGIN RAISE NOTICE 'x;'; END $$;
SELECT $1::int;
;;`
	got := splitStatements(sql)
	require.Len(t, got, 5, "%q", got)
	assert.Contains(t, got[1], "$body$ BEGIN PERFORM 1; COMMIT; END $body$")
	assert.Contains(t, got[2], `E'it\'s; fine'`)
	assert.Equal(t, "SELECT $1::int", got[4])
	assert.Empty(t, splitStatements("  ;  -- only a comment\n"))
	assert.Len(t, splitStatements("SELECT 'unterminated; still one"), 1)
	assert.Len(t, splitStatements("SELECT $tag$ never closed; still one"), 1)
	assert.Len(t, splitStatements("SELECT 1 /* never closed; still one"), 1)
	assert.Len(t, splitStatements("SELECT $ 1; SELECT 2"), 2, "a lone $ quotes nothing")
}

func TestCheckMigrationSQL(t *testing.T) {
	require.NoError(t, checkMigrationSQL(`CREATE TABLE a (id int);
CREATE PROCEDURE p() LANGUAGE plpgsql AS $$ BEGIN COMMIT; END $$;`, true))
	for _, sql := range []string{
		"CREATE TABLE a (id int); COMMIT; DROP TABLE b",
		"begin; create table a (id int)",
		"CREATE TABLE a (id int);\n  -- x\n  END",
		"SAVEPOINT s",
		"ROLLBACK",
	} {
		require.Error(t, checkMigrationSQL(sql, true), sql)
	}
	require.NoError(t, checkMigrationSQL("CREATE INDEX CONCURRENTLY i ON a (id)", false))
	require.ErrorContains(t, checkMigrationSQL("CREATE INDEX CONCURRENTLY i ON a (id); SELECT 1", false), "exactly one statement")
}

func TestAddRefusesSQLThatEndsItsTransaction(t *testing.T) {
	tk, _ := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	_, text := run(t.Context(), t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "x", Up: "CREATE TABLE a (id int); COMMIT; DROP TABLE b"})
	assert.Contains(t, text, "statement 2 is COMMIT")
	_, text = run(t.Context(), t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "x", Up: "CREATE TABLE a (id int)", Down: "ROLLBACK"})
	assert.Contains(t, text, "down SQL")
	no := false
	_, text = run(t.Context(), t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "x", Up: "CREATE INDEX CONCURRENTLY i ON a (id); CREATE INDEX CONCURRENTLY j ON a (id)", Transactional: &no})
	assert.Contains(t, text, "exactly one statement")
}

func TestPlanOneEncodesAndLaysOutTheRows(t *testing.T) {
	sd := &pgconn.StatementDescription{Name: "stmt_x", ParamOIDs: []uint32{oidInt4}, Fields: []pgconn.FieldDescription{field("n", oidText)}}
	types := []typeInfo{fakeTypes()[oidInt4], fakeTypes()[oidText]}
	plan, values, err := planOne(0, sd, []any{json.Number("7")}, types, 10)
	require.NoError(t, err)
	assert.Equal(t, "stmt_x", plan.name)
	assert.Equal(t, [][]byte{[]byte("7")}, values)
	assert.Equal(t, []Column{{Name: "n", Type: "text"}}, plan.read.set.columns)
	assert.Equal(t, 10, plan.read.limit)
	_, _, err = planOne(1, &pgconn.StatementDescription{ParamOIDs: []uint32{oidBytea}}, []any{"not base64!"}, []typeInfo{fakeTypes()[oidBytea]}, 10)
	require.ErrorContains(t, err, "statement 2 of the batch")
}

func TestRevertMigrations(t *testing.T) {
	applied := []Applied{{Version: 1}, {Version: 2}}
	ms := []Migration{{Version: 1, Down: "DROP TABLE a"}, {Version: 2, Down: "DROP TABLE b"}}
	got, err := revertMigrations(applied, ms, -1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Version)
	got, err = revertMigrations(applied, ms, 0)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	ms[1].Down = ""
	_, err = revertMigrations(applied, ms, -1)
	require.ErrorContains(t, err, "no down SQL")
}

func TestPlanOneRefusesMismatchedTypes(t *testing.T) {
	_, _, err := planOne(0, &pgconn.StatementDescription{ParamOIDs: []uint32{oidInt4}}, []any{"1"}, nil, 10)
	require.ErrorContains(t, err, "0 types for 1 parameters")
}
