package trino

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

func declaredOf(t *testing.T, b []byte) []string {
	t.Helper()
	cols, err := tableparquet.Columns(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Name+" "+c.Type.SQL())
	}
	return out
}

// TestParquetFormatter_InfersAScriptsColumns pins that rows with no declared
// types are typed by the rules a JSON-lines registration uses (#1833).
func TestParquetFormatter_InfersAScriptsColumns(t *testing.T) {
	f, err := NewFormatter("parquet")
	require.NoError(t, err)
	assert.Equal(t, tableparquet.ContentType, f.ContentType())
	assert.Equal(t, ".parquet", f.FileExtension())

	b, err := f.Format([]string{"id", "amount", "ok", "who", "tags", "shop", "never"}, [][]any{
		{int64(1), 1.5, true, "a", []any{"x"}, map[string]any{"n": "N", "s": int64(3)}, nil},
		{2, int64(2), false, nil, []any{}, map[string]any{"n": "S"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"id BIGINT", "amount DOUBLE", "ok BOOLEAN", "who VARCHAR", "tags ARRAY(VARCHAR)",
		`shop ROW("n" VARCHAR, "s" BIGINT)`, "never VARCHAR",
	}, declaredOf(t, b))
}

func TestParquetFormatter_RefusesWhatNoTableReads(t *testing.T) {
	f := &parquetFormatter{}
	_, err := f.Format([]string{"x"}, [][]any{{map[string]any{"a": 1}}, {[]any{1}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `row 2: the value of "x" is a list here and was an object`)

	_, err = f.Format([]string{"x"}, [][]any{{"bad \xff utf8"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid UTF-8")

	_, err = f.FormatTyped([]tabletype.Column{
		{Name: "Id", Type: tabletype.Scalar(tabletype.Bigint)}, {Name: "id", Type: tabletype.Scalar(tabletype.Varchar)},
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the columns "Id" and "id" are one column`)
}

func TestColumnTypes_ReadsWhatTheDriverReports(t *testing.T) {
	cols, err := columnTypes([]trinoclient.ColumnInfo{
		{Name: "amount", Type: "DECIMAL", Precision: 12, Scale: 2},
		{Name: "at", Type: "TIMESTAMP", Precision: 6},
		{Name: "tags", Type: "ARRAY(ROW(A BIGINT))"},
	})
	require.NoError(t, err)
	assert.Equal(t, "DECIMAL(12,2)", cols[0].Type.SQL())
	assert.Equal(t, 6, cols[1].Type.Precision)
	assert.Equal(t, `ARRAY(ROW("a" BIGINT))`, cols[2].Type.SQL())

	_, err = columnTypes([]trinoclient.ColumnInfo{{Name: "x", Type: "ARRAY("}})
	assert.Error(t, err)
}

func TestStorageNote_SaysWhatParquetCouldNotKeep(t *testing.T) {
	uuid, err := tabletype.Parse("UUID")
	require.NoError(t, err)
	note := storageNote([]tabletype.Column{
		{Name: "tz", Type: tabletype.Scalar(tabletype.TimestampTZ)},
		{Name: "u", Type: uuid},
		{Name: "id", Type: tabletype.Scalar(tabletype.Bigint)},
	})
	assert.Contains(t, note, "tz read back in UTC")
	assert.Contains(t, note, "written as text: u (uuid)")
	assert.Empty(t, storageNote([]tabletype.Column{{Name: "id", Type: tabletype.Scalar(tabletype.Bigint)}}))
}

// TestStorageNote_NamesEveryLossAtEveryDepth: a nested column loses the same
// things a top-level one does, and a timestamp finer than a microsecond is
// written to the microsecond. A loss that went unnamed beside one that was
// named would read as a column that lost nothing.
func TestStorageNote_NamesEveryLossAtEveryDepth(t *testing.T) {
	for _, text := range []string{
		`row("when" timestamp(6) with time zone, "who" uuid)`,
		"array(timestamp(6) with time zone)",
		"map(varchar, timestamp(6) with time zone)",
	} {
		parsed, err := tabletype.Parse(text)
		require.NoError(t, err, text)
		note := storageNote([]tabletype.Column{{Name: "c", Type: parsed}})
		assert.Contains(t, note, "read back in UTC", text)
	}

	nested, err := tabletype.Parse(`row("when" timestamp(6) with time zone, "who" uuid)`)
	require.NoError(t, err)
	note := storageNote([]tabletype.Column{{Name: "c", Type: nested}})
	assert.Contains(t, note, "c.when read back in UTC")
	assert.Contains(t, note, "c.who (uuid)")

	fine := tabletype.Type{Kind: tabletype.Timestamp, Precision: 9}
	note = storageNote([]tabletype.Column{
		{Name: "ns", Type: fine},
		{Name: "us", Type: tabletype.Type{Kind: tabletype.Timestamp, Precision: 6}},
	})
	assert.Contains(t, note, "finer digits are dropped: ns.")
	assert.NotContains(t, note, "us")
}

func TestFormatQueryResult_WritesParquetFromTheQuerysTypes(t *testing.T) {
	at := time.Date(2024, 5, 1, 12, 34, 56, 789123000, time.UTC)
	result := &trinoclient.QueryResult{
		Columns: []trinoclient.ColumnInfo{{Name: "id", Type: "BIGINT"}, {Name: "at", Type: "TIMESTAMP", Precision: 6}},
		Rows:    []map[string]any{{"id": int64(1), "at": at}},
	}
	out, errResult := formatQueryResult(formatParquet, result, queryRows(result), 1<<20)
	require.Nil(t, errResult)
	assert.Equal(t, tableparquet.ContentType, out.formatter.ContentType())
	assert.Empty(t, out.note)
	assert.Equal(t, []string{"id BIGINT", "at TIMESTAMP(6)"}, declaredOf(t, out.body))

	_, errResult = formatQueryResult(formatParquet, result, queryRows(result), 10)
	require.NotNil(t, errResult, "a file over the byte cap is refused")

	bad := &trinoclient.QueryResult{Columns: []trinoclient.ColumnInfo{{Name: "x", Type: "MAP("}}}
	_, errResult = formatQueryResult(formatParquet, bad, nil, 1<<20)
	require.NotNil(t, errResult, "a type the parse cannot read is refused")

	csv, errResult := formatQueryResult(formatCSV, result, queryRows(result), 1<<20)
	require.Nil(t, errResult)
	assert.Contains(t, string(csv.body), "id,at")
}

func TestNonEmpty(t *testing.T) {
	assert.Equal(t, []string{"a", "c"}, nonEmpty("a", "", "c"))
	assert.Empty(t, nonEmpty("", ""))
}
