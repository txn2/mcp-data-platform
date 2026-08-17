package sqltables

import "testing"

// A JOIN's second table used to be invisible: the extractor's optional alias
// group swallowed the JOIN keyword as if it were the first table's alias, and
// the scan resumed past it. Every enrichment of a joined query was therefore
// missing half its tables, and a recorded call's targets would have been too.
func TestExtractNamesBothSidesOfAJoin(t *testing.T) {
	t.Parallel()

	got := Extract("SELECT * FROM iceberg.sales.orders JOIN iceberg.sales.regions ON orders.region_id = regions.id")
	if len(got) != 2 {
		t.Fatalf("extracted %d tables, want both sides of the join: %+v", len(got), got)
	}
	if got[0].Table != "orders" || got[1].Table != "regions" {
		t.Errorf("tables = %+v", got)
	}
}

func TestExtractSkipsFunctionsAndKeywords(t *testing.T) {
	t.Parallel()

	// A name followed by "(" is a call, not a table, and the words that can
	// follow FROM without naming one are named rather than guessed at.
	for _, sql := range []string{
		"SELECT * FROM UNNEST(ARRAY[1,2])",
		"SELECT * FROM (SELECT 1)",
		"SELECT * FROM TABLE(system.gen(1))",
	} {
		if got := Extract(sql); len(got) != 0 {
			t.Errorf("%q extracted %+v, want nothing", sql, got)
		}
	}
}

func TestExtractDropsCommonTableExpressions(t *testing.T) {
	t.Parallel()

	// A CTE is not a physical table: enriching it would describe a name that
	// exists only inside the statement.
	got := Extract("WITH recent AS (SELECT * FROM iceberg.sales.orders) SELECT * FROM recent")
	if len(got) != 1 || got[0].Table != "orders" {
		t.Errorf("tables = %+v, want only the physical one", got)
	}
}
