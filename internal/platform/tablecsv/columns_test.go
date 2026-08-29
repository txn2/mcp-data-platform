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
