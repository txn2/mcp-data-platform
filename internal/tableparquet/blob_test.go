package tableparquet

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// TestWrite_ABlobIsStoredOnce holds that a VARBINARY value is written once.
// Page statistics copy a page's lowest and highest value, so a page of one large
// value used to reach the file five times: 270 MiB of blobs made a 1.35 GB file.
func TestWrite_ABlobIsStoredOnce(t *testing.T) {
	blob := make([]byte, 1<<20)
	_, err := rand.Read(blob)
	require.NoError(t, err)

	columns := []tabletype.Column{
		{Name: "blob", Type: tabletype.Scalar(tabletype.Varbinary)},
		{Name: "blobs", Type: tabletype.ArrayOf(tabletype.Scalar(tabletype.Varbinary))},
		{Name: "name", Type: tabletype.Scalar(tabletype.Varchar)},
	}
	b, err := Write(columns, [][]any{{blob, []any{blob}, "ann"}})
	require.NoError(t, err)

	// Two values of one MiB, one copy each, and room for the file's own structure.
	assert.Less(t, len(b), 2*len(blob)+1<<18, "a blob reached the file more than once")

	f, err := parquet.OpenFile(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)

	indexes := f.ColumnIndexes()
	require.Len(t, indexes, 3)
	assert.Empty(t, indexes[0].MinValues[0], "a bound of a top-level blob was kept")
	assert.Empty(t, indexes[1].MinValues[0], "a bound of a nested blob was kept")
	assert.Equal(t, "ann", string(indexes[2].MinValues[0]), "text keeps its bounds, which prune pages")

	rows := f.RowGroups()[0].Rows()
	defer func() { require.NoError(t, rows.Close()) }()
	read := make([]parquet.Row, 1)
	n, err := rows.ReadRows(read)
	if !errors.Is(err, io.EOF) {
		require.NoError(t, err)
	}
	require.Equal(t, 1, n)
	assert.True(t, bytes.Equal(blob, valuesByColumn(read[0])[0][0].ByteArray()), "the blob did not read back")
}
