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
