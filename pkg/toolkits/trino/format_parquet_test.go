package trino

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
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

// nestedTrinoColumns are a ROW, a ROW inside an ARRAY, a ROW inside a MAP and
// an ARRAY of VARBINARY, in the form Trino 453 reports them.
const nestedTrinoColumns = `[
 {"name":"r","type":"row(a bigint, b varchar)","typeSignature":{"rawType":"row","arguments":[
  {"kind":"NAMED_TYPE","value":{"fieldName":{"name":"a"},"typeSignature":{"rawType":"bigint","arguments":[]}}},
  {"kind":"NAMED_TYPE","value":{"fieldName":{"name":"b"},"typeSignature":{"rawType":"varchar","arguments":[{"kind":"LONG","value":2147483647}]}}}]}},
 {"name":"ra","type":"array(row(a bigint, d date))","typeSignature":{"rawType":"array","arguments":[{"kind":"TYPE","value":{"rawType":"row","arguments":[
  {"kind":"NAMED_TYPE","value":{"fieldName":{"name":"a"},"typeSignature":{"rawType":"bigint","arguments":[]}}},
  {"kind":"NAMED_TYPE","value":{"fieldName":{"name":"d"},"typeSignature":{"rawType":"date","arguments":[]}}}]}}]}},
 {"name":"m","type":"map(varchar, row(x double))","typeSignature":{"rawType":"map","arguments":[
  {"kind":"TYPE","value":{"rawType":"varchar","arguments":[{"kind":"LONG","value":2147483647}]}},
  {"kind":"TYPE","value":{"rawType":"row","arguments":[
   {"kind":"NAMED_TYPE","value":{"fieldName":{"name":"x"},"typeSignature":{"rawType":"double","arguments":[]}}}]}}]}},
 {"name":"vb","type":"array(varbinary)","typeSignature":{"rawType":"array","arguments":[{"kind":"TYPE","value":{"rawType":"varbinary","arguments":[]}}]}}
]`

// nestedTrino answers one statement with nestedTrinoColumns and one row of
// them: the POST is queued, and the GET of its nextUri finishes with the data.
func nestedTrino(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("POST /v1/statement", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"q1","nextUri":"%s/v1/statement/q1/1","stats":{"state":"QUEUED"}}`, srv.URL)
	})
	mux.HandleFunc("GET /v1/statement/q1/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"q1","columns":%s,"data":[[[1,"two"],[[2,"2026-10-16"]],{"k":["NaN"]},["AAH/"]]],"stats":{"state":"FINISHED"}}`,
			nestedTrinoColumns)
	})
	return srv
}

// TestFormatQueryResult_WritesTheDriversNestedValues covers #2183: trino_export
// reads a Parquet export's rows raw, and from trino-go-client v1.2 the driver
// decodes a ROW into a trino.Row and the values inside a container into Go
// types. Through the real driver, each is written as the value Trino sent.
func TestFormatQueryResult_WritesTheDriversNestedValues(t *testing.T) {
	u, err := url.Parse(nestedTrino(t).URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	client, err := trinoclient.New(trinoclient.Config{Host: u.Hostname(), Port: port, User: "u", Source: "test", Timeout: 10 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.Query(context.Background(), "SELECT 1", trinoclient.QueryOptions{Limit: 10, RawValues: true})
	require.NoError(t, err)
	out, errResult := formatQueryResult(formatParquet, result, queryRows(result), 1<<20)
	require.Nil(t, errResult)
	assert.Equal(t, []string{
		`r ROW("a" BIGINT, "b" VARCHAR)`, `ra ARRAY(ROW("a" BIGINT, "d" DATE))`,
		`m MAP(VARCHAR, ROW("x" DOUBLE))`, "vb ARRAY(VARBINARY)",
	}, declaredOf(t, out.body))

	read := make([]parquet.Row, 1)
	n, _ := parquet.NewReader(bytes.NewReader(out.body)).ReadRows(read)
	require.Equal(t, 1, n)
	leaves := map[int]parquet.Value{}
	for _, v := range read[0] {
		leaves[v.Column()] = v
	}
	day := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC).Unix() / (24 * 60 * 60)
	assert.Equal(t, int64(1), leaves[0].Int64(), "r.a")
	assert.Equal(t, "two", string(leaves[1].ByteArray()), "r.b")
	assert.Equal(t, int64(2), leaves[2].Int64(), "ra[].a")
	assert.Equal(t, day, int64(leaves[3].Int32()), "ra[].d is the date Trino sent")
	assert.Equal(t, "k", string(leaves[4].ByteArray()), "the map key")
	assert.True(t, math.IsNaN(leaves[5].Double()), "m[k].x")
	assert.Equal(t, []byte{0x00, 0x01, 0xff}, leaves[6].ByteArray(), "vb[]")
}
