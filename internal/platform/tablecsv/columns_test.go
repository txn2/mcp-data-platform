package tablecsv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestColumnsFrom pins how a header record becomes column names: the BOM a
// spreadsheet leads with is dropped, a blank name is filled in positionally,
// a repeated name is suffixed, and the suffix itself can never collide.
func TestColumnsFrom(t *testing.T) {
	cols := ColumnsFrom([]string{bomUTF8 + "store_id", " ", "amount", "Amount", "amount_2", "amount"})
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		assert.Equal(t, ColumnType, c.Type, "Hive CSV admits one column type")
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"store_id", "column_2", "amount", "Amount_2", "amount_2_2", "amount_3"}, names)
}

// TestUncorrectable: the refusal a file that cannot be corrected carries reads
// as its reason alone and answers to the sentinel.
func TestUncorrectable(t *testing.T) {
	err := uncorrectablef("this file cannot be corrected because %s", "of a ragged row")
	assert.EqualError(t, err, "this file cannot be corrected because of a ragged row")
	assert.ErrorIs(t, err, ErrUncorrectable)
}

// TestColumnsFrom_ACommaIsDroppedFromTheName. The Hive metastore stores a
// table's column list comma-separated, so the connector refuses a column name
// holding one -- "Hive column names must not contain commas" -- and no quoting
// gets past it. A Facebook Insights export names a column "Reactions, Comments
// and Shares", so no export from that surface could be registered even once
// its byte-order mark stopped hiding the header (#1774).
func TestColumnsFrom_ACommaIsDroppedFromTheName(t *testing.T) {
	cols := ColumnsFrom([]string{
		"Reactions, Comments and Shares",
		"a,b",
		",",
		"Post ID",
		"x,,y",
	})
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{
		"Reactions Comments and Shares",
		"a b",
		// Nothing but commas leaves no name, which is the blank case.
		"column_3",
		"Post ID",
		"x y",
	}, names)
}

// TestWithoutCommas: a name with none is returned untouched, so the common
// case allocates nothing and no other character is disturbed. Every one of
// these was created on Trino 476 without complaint while a comma was refused.
func TestWithoutCommas(t *testing.T) {
	for _, name := range []string{"Post ID", "a.b", "a:b", "a(b)", "a/b", "a;b", "a%b", "a=b", "a-b"} {
		assert.Equal(t, name, withoutCommas(name))
	}
	assert.Equal(t, "a b", withoutCommas("a, b"))
	assert.Equal(t, "a b", withoutCommas("a ,b"))
	assert.Empty(t, withoutCommas(",,,"))
}

func TestColumnChanges(t *testing.T) {
	before := []Column{{Name: "a", Type: "BIGINT"}, {Name: "b", Type: "VARCHAR"}, {Name: "c", Type: "DATE"}}
	after := []Column{{Name: "c", Type: "DATE"}, {Name: "a", Type: "DOUBLE"}, {Name: "d", Type: "BOOLEAN"}}
	assert.Equal(t, "added d BOOLEAN; removed b; a is now DOUBLE (was BIGINT)", ColumnChanges(before, after))
	assert.Empty(t, ColumnChanges(before, before))
	assert.Empty(t, ColumnChanges(before, []Column{before[2], before[0], before[1]}), "a reorder is not a change")
}

func TestColumnNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, ColumnNames([]Column{{Name: "a", Type: "BIGINT"}, {Name: "b"}}))
	assert.Equal(t, []string{}, ColumnNames(nil), "no columns is an empty list, never null")
}

// TestVarcharColumns pins the rule a JSON-lines registration made before typed
// columns keeps: every scalar is declared VARCHAR, and a nested value is
// refused naming the column.
func TestVarcharColumns(t *testing.T) {
	got, err := VarcharColumns([]Column{{Name: "id", Type: "BIGINT"}, {Name: "ok", Type: "BOOLEAN"}})
	assert.NoError(t, err)
	assert.Equal(t, []Column{{Name: "id", Type: ColumnType}, {Name: "ok", Type: ColumnType}}, got)

	for _, nested := range []string{"ROW(x BIGINT)", "ARRAY(VARCHAR)", "MAP(VARCHAR, BIGINT)"} {
		_, err := VarcharColumns([]Column{{Name: "id", Type: "BIGINT"}, {Name: "blob", Type: nested}})
		assert.ErrorContains(t, err, `the value of "blob" is a nested object or list`, nested)
	}
}
