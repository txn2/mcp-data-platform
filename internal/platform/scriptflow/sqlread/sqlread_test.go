package sqlread

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWritesOf is the tables a statement writes, by its verb, and the
// positions a verb is not a write in.
func TestWritesOf(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []Write
	}{
		{"a plain insert", "INSERT INTO s.t (a) VALUES (1)", []Write{{"INSERT", "s.t"}}},
		{
			"a CTE insert and the statement's own", "with a as (insert into s.x values (1) returning id) update s.y set n = 1 from a",
			[]Write{{"INSERT", "s.x"}, {"UPDATE", "s.y"}},
		},
		{
			"two CTEs, one deleting", "WITH a AS (DELETE FROM s.old RETURNING *), b AS (SELECT 1) INSERT INTO s.archive SELECT * FROM a",
			[]Write{{"DELETE", "s.old"}, {"INSERT", "s.archive"}},
		},
		{
			"ON CONFLICT DO UPDATE is not a second write", "INSERT INTO s.t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 1",
			[]Write{{"INSERT", "s.t"}},
		},
		{
			"a CTE upsert", "WITH a AS (INSERT INTO s.t (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 1 RETURNING id) SELECT * FROM a",
			[]Write{{"INSERT", "s.t"}},
		},
		{"SELECT FOR UPDATE writes nothing", "WITH a AS (SELECT * FROM s.t FOR UPDATE) SELECT * FROM a", nil},
		{"a read", "SELECT * FROM s.t", nil},
		// #2840: a statement is read by where its words stand, not by a
		// comment in front of it or a verb inside one.
		{
			"a WITH statement opening with a comment",
			"-- record the change beside the rate\nWITH b AS MATERIALIZED (SELECT 1 AS id) INSERT INTO s.rate_change SELECT id FROM b",
			[]Write{{"INSERT", "s.rate_change"}},
		},
		{
			"a block comment in front of the verb", "/* nightly */ UPDATE s.stock SET n = 0",
			[]Write{{"UPDATE", "s.stock"}},
		},
		{
			"a verb in a comment is not a write", "WITH a AS (SELECT 1 /* (DELETE FROM s.gone */) SELECT * FROM a",
			nil,
		},
		{
			"a -- inside a literal is not a comment", "WITH a AS (SELECT '--' AS x) INSERT INTO s.t SELECT x FROM a",
			[]Write{{"INSERT", "s.t"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, WritesOf(tt.sql))
		})
	}
}

// TestOutputRefs is what each output step names (#2540).

func TestSchemaOf(t *testing.T) {
	assert.Equal(t, "public", SchemaOf("orders"))
	assert.Equal(t, "shop", SchemaOf("shop.orders"))
	assert.Equal(t, "shop", SchemaOf(`"shop"."orders"`))
	assert.Equal(t, "s", SchemaOf("hive.s.t"))
}

func TestTablesInAndHoles(t *testing.T) {
	assert.Equal(t, []string{"warehouse.public.{t}", "s.u"}, TablesIn("SELECT * FROM warehouse.public.{t} JOIN s.u ON true"))
	assert.True(t, HasHole([]string{"a", "s.{x}"}))
	assert.False(t, HasHole([]string{"a"}))
	w, ok := LastWrite("WITH x AS (DELETE FROM s.old RETURNING *) INSERT INTO s.archive SELECT * FROM x")
	assert.True(t, ok)
	assert.Equal(t, Write{Verb: "INSERT", Table: "s.archive"}, w)
	_, ok = LastWrite("SELECT 1")
	assert.False(t, ok)
	assert.Empty(t, WritesOf("WITH x AS (SELECT 1) SELECT * FROM x"))
}

// TestTablesIn_TheTicketStatement is #2840's statement: its WITH names are not
// tables, its writes are its CTE's target and its own, and a table named only
// in a comment is not read.
func TestTablesIn_TheTicketStatement(t *testing.T) {
	sql := `-- one batch id for the whole change
WITH b AS MATERIALIZED (SELECT gen_random_uuid() AS id),
old AS (SELECT r.container_id, jsonb_build_object('a', r.a) AS v FROM container_rate r JOIN container_grid g ON g.id = r.grid_id),
up AS (
    INSERT INTO container_rate (container_id, a) SELECT c.id, $1 FROM container c
    ON CONFLICT (container_id) DO UPDATE SET a = EXCLUDED.a
    RETURNING container_id, a
)
INSERT INTO rate_change (batch_id, container_id, old_values, new_values)
SELECT b.id, up.container_id, old.v, jsonb_build_object('a', up.a)
  FROM up
 CROSS JOIN b -- not FROM legacy_rates
  LEFT JOIN old ON old.container_id = up.container_id
RETURNING container_id, batch_id::text AS batch_id`
	assert.Equal(t, []Write{{"INSERT", "container_rate"}, {"INSERT", "rate_change"}}, WritesOf(sql))
	assert.Equal(t, []string{"container_rate", "container_grid", "container"}, TablesIn(sql))
}

// issue2849Rule is #2849's statement: a proposed rule whose title the
// statement builds from text holding "from a correction", written only when
// no rule of that title exists.
const issue2849Rule = `INSERT INTO x3ap.rule (kind, title, body)
SELECT 'coding',
       coalesce(s.name, 'a supplier') || ' in ' || :company_code || ': ' || x.what
           || ' (from a correction on invoice ' || coalesce(nullif(:invoice_number, ''), 'without a number') || ')',
       x.what
FROM x3ap.x3_supplier s, (SELECT 'join the queue' AS what) x
WHERE NOT EXISTS (SELECT 1 FROM x3ap.rule r WHERE r.title = 'from a correction')`

// TestTablesIn_TextInsideLiteralsIsNotRead is #2849: "from <word>" or
// "join <word>" in a string literal, an E-string or a dollar-quoted body is
// text, not a table, and a verb in one is not a write.
func TestTablesIn_TextInsideLiteralsIsNotRead(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{"a literal holding from", "SELECT 'see the note from a correction' AS t FROM x3ap.rule", []string{"x3ap.rule"}},
		{"a doubled quote inside the literal", "SELECT 'it''s from a fix, join b' AS t FROM s.t", []string{"s.t"}},
		{"an E-string with an escaped quote", `SELECT E'from a\' join b' AS t FROM s.t`, []string{"s.t"}},
		{"a dollar-quoted body", "SELECT $$from a join b$$ AS t FROM s.t", []string{"s.t"}},
		{"a tagged dollar-quoted body", "SELECT $note$ from a $note$ AS t FROM s.t", []string{"s.t"}},
		{"a positional parameter is not a dollar quote", "SELECT * FROM s.t WHERE id = $1 AND n = $2", []string{"s.t"}},
		{"a computed part with a quote in its source", "SELECT * FROM s.{t['name']}", []string{"s.{t['name']}"}},
		{"the ticket's rule", issue2849Rule, []string{"x3ap.rule", "x3ap.x3_supplier"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.want, TablesIn(tt.sql))
		})
	}
	assert.Equal(t, []Write{{"INSERT", "x3ap.rule"}}, WritesOf(issue2849Rule), "the rule's statement writes x3ap.rule")
	assert.Nil(t, WritesOf("WITH a AS (SELECT '(DELETE FROM s.gone' AS x) SELECT * FROM a"), "a verb in a literal is not a write")
	assert.Nil(t, WritesOf("WITH a AS (SELECT $$ (UPDATE s.gone $$ AS x) SELECT * FROM a"), "a verb in a dollar body is not a write")
}

// TestQuotedNamesAreTheTablesTheyName: a quoted table name is read and
// written as the table it names, without its quotes, so an application
// matches it to the table its schema lists (#2849).
func TestQuotedNamesAreTheTablesTheyName(t *testing.T) {
	assert.Equal(t, []string{"Sales.Orders", "s.line"}, TablesIn(`SELECT * FROM "Sales"."Orders" o JOIN s."line" l ON l.o = o.id`))
	assert.Equal(t, []string{`odd"name`}, TablesIn(`SELECT * FROM "odd""name"`))
	assert.Equal(t, []Write{{"INSERT", "Sales.Orders"}}, WritesOf(`INSERT INTO "Sales"."Orders" (id) VALUES (1)`))
	assert.Equal(t, []Write{{"DELETE", "s.Old"}, {"INSERT", "s.archive"}},
		WritesOf(`WITH a AS (DELETE FROM s."Old" RETURNING *) INSERT INTO s.archive SELECT * FROM a`))
	assert.Equal(t, "Sales", SchemaOf("Sales.Orders"))
}
