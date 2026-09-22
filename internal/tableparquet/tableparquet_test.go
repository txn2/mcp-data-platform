package tableparquet

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/deprecated"
	"github.com/parquet-go/parquet-go/encoding/thrift"
	"github.com/parquet-go/parquet-go/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// fileOf writes an empty Parquet file with the given top-level fields, in
// order. Columns reads only the footer, so a file with no rows is enough to
// pin how a schema is declared.
func fileOf(t *testing.T, fields ...any) []byte {
	t.Helper()
	root := group(fields...)
	var buf bytes.Buffer
	w := parquet.NewWriter(&buf, parquet.NewSchema("t", root))
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func columnsOfFile(t *testing.T, b []byte) ([]tabletype.Column, error) {
	t.Helper()
	return Columns(bytes.NewReader(b), int64(len(b)))
}

func declared(cols []tabletype.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Name+" "+c.Type.SQL())
	}
	return out
}

// group builds an ordered group from name, node pairs.
func group(pairs ...any) *orderedGroup {
	g := &orderedGroup{}
	for len(pairs) >= 2 {
		name, _ := pairs[0].(string)
		node, _ := pairs[1].(parquet.Node)
		g.fields = append(g.fields, field{Node: node, name: name})
		pairs = pairs[2:]
	}
	return g
}

func TestColumns_MapsEverySupportedType(t *testing.T) {
	b := fileOf(t,
		"B", parquet.Optional(parquet.Leaf(parquet.BooleanType)),
		"i32", parquet.Optional(parquet.Leaf(parquet.Int32Type)),
		"i8", parquet.Optional(parquet.Int(8)),
		"i16", parquet.Optional(parquet.Int(16)),
		"i32s", parquet.Optional(parquet.Int(32)),
		"i64", parquet.Optional(parquet.Leaf(parquet.Int64Type)),
		"i64s", parquet.Optional(parquet.Int(64)),
		"u8", parquet.Optional(parquet.Uint(8)),
		"u16", parquet.Optional(parquet.Uint(16)),
		"f", parquet.Optional(parquet.Leaf(parquet.FloatType)),
		"d", parquet.Optional(parquet.Leaf(parquet.DoubleType)),
		"dec32", parquet.Optional(parquet.Decimal(2, 9, parquet.Int32Type)),
		"dec64", parquet.Optional(parquet.Decimal(2, 12, parquet.Int64Type)),
		"decflba", parquet.Optional(parquet.Decimal(10, 38, parquet.FixedLenByteArrayType(16))),
		"decba", parquet.Optional(parquet.Decimal(3, 20, parquet.ByteArrayType)),
		"s", parquet.Optional(parquet.String()),
		"e", parquet.Optional(parquet.Enum()),
		"j", parquet.Optional(parquet.JSON()),
		"bin", parquet.Optional(parquet.Leaf(parquet.ByteArrayType)),
		"dt", parquet.Optional(parquet.Date()),
		"tsms", parquet.Optional(parquet.Timestamp(parquet.Millisecond)),
		"tsus", parquet.Optional(parquet.TimestampAdjusted(parquet.Microsecond, false)),
		"tsns", parquet.Optional(parquet.Timestamp(parquet.Nanosecond)),
		"i96", parquet.Optional(parquet.Leaf(parquet.Int96Type)),
		"l", parquet.Optional(parquet.List(parquet.Optional(parquet.Int(64)))),
		"m", parquet.Optional(parquet.Map(parquet.String(), parquet.Optional(parquet.Leaf(parquet.DoubleType)))),
		"r", parquet.Optional(group("A", parquet.Optional(parquet.Int(32)), "n", parquet.Optional(group("x", parquet.String())))),
	)
	cols, err := columnsOfFile(t, b)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"b BOOLEAN", "i32 INTEGER", "i8 TINYINT", "i16 SMALLINT", "i32s INTEGER", "i64 BIGINT", "i64s BIGINT",
		"u8 SMALLINT", "u16 INTEGER", "f REAL", "d DOUBLE",
		"dec32 DECIMAL(9,2)", "dec64 DECIMAL(12,2)", "decflba DECIMAL(38,10)", "decba DECIMAL(20,3)",
		"s VARCHAR", "e VARCHAR", "j VARCHAR", "bin VARBINARY", "dt DATE",
		"tsms TIMESTAMP(6)", "tsus TIMESTAMP(6)", "tsns TIMESTAMP(6)", "i96 TIMESTAMP(6)",
		"l ARRAY(BIGINT)", "m MAP(VARCHAR, DOUBLE)", `r ROW("a" INTEGER, "n" ROW("x" VARCHAR))`,
	}, declared(cols))
}

func TestColumns_RefusesATypeNothingReadsBackExactly(t *testing.T) {
	tests := []struct {
		name string
		node parquet.Node
		want string
	}{
		{"time", parquet.Optional(parquet.Time(parquet.Microsecond)), `the column "c" is a Parquet INT64 TIME(`},
		{"uuid", parquet.Optional(parquet.UUID()), `the column "c" is a Parquet FIXED_LEN_BYTE_ARRAY(16) UUID`},
		{"plain fixed", parquet.Optional(parquet.Leaf(parquet.FixedLenByteArrayType(4))), "FIXED_LEN_BYTE_ARRAY(4)"},
		{"an unsigned 64-bit integer", parquet.Optional(parquet.Uint(64)), `the column "c" is a Parquet INT64 INT(64,false)`},
		{"an unsigned 32-bit integer", parquet.Optional(parquet.Uint(32)), `the column "c" is a Parquet INT32 INT(32,false)`},
		{"a repeated primitive", parquet.Repeated(parquet.Int(32)), "a repeated field outside a LIST or MAP group"},
		{"a nested time", parquet.Optional(parquet.List(parquet.Optional(parquet.Time(parquet.Millisecond)))), `"c[]" is a Parquet INT32 TIME(`},
		{"a nested name the metastore cannot store", parquet.Optional(group("a-b", parquet.String())), `the nested key "a-b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := columnsOfFile(t, fileOf(t, "ok", parquet.Optional(parquet.String()), "c", tt.node))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestColumns_RefusesColumnsOneApartByCase(t *testing.T) {
	_, err := columnsOfFile(t, fileOf(t, "Id", parquet.Optional(parquet.Int(64)), "id", parquet.Optional(parquet.String())))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the columns "Id" and "id" are one column`)

	_, err = columnsOfFile(t, fileOf(t, "r", parquet.Optional(group("X", parquet.String(), "x", parquet.String()))))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the fields "X" and "x" are one field`)
}

func TestColumns_RefusesBytesThatAreNotParquet(t *testing.T) {
	for name, b := range map[string][]byte{
		"text":      []byte("id,name\n1,a\n"),
		"too short": []byte("PAR1PAR1"),
		"one magic": append([]byte("PAR1"), make([]byte, 20)...),
		"no footer": append(append([]byte("PAR1"), make([]byte, 20)...), []byte("\x05\x00\x00\x00PAR1")...),
	} {
		_, err := columnsOfFile(t, b)
		assert.True(t, errors.Is(err, ErrNotParquet), "%s: %v", name, err)
	}
	enc := append(append([]byte("PAR1"), make([]byte, 20)...), []byte("PARE")...)
	_, err := columnsOfFile(t, enc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypted")
}

// TestColumns_RefusesAFooterLongerThanTheFile holds the footer length against
// the file it came from. The reader allocates the length before reading it, so
// these sixteen bytes would otherwise allocate 4 GiB to fail.
func TestColumns_RefusesAFooterLongerThanTheFile(t *testing.T) {
	for name, length := range map[string][]byte{
		"4 GiB":            {0xFF, 0xFF, 0xFF, 0xFF},
		"one byte too far": {0x09, 0x00, 0x00, 0x00},
		"empty":            {0x00, 0x00, 0x00, 0x00},
	} {
		b := append(append([]byte("PAR1"), make([]byte, 4)...), length...)
		b = append(b, []byte("PAR1")...)
		require.Len(t, b, 16, name)
		_, err := columnsOfFile(t, b)
		require.Error(t, err, name)
		assert.True(t, errors.Is(err, ErrNotParquet), "%s: %v", name, err)
		assert.Contains(t, err.Error(), "does not fit in a file of 16 bytes", name)
	}
}

func TestColumns_ColumnCountBounds(t *testing.T) {
	fields := make([]any, 0, 2*(maxColumns+1))
	for i := range maxColumns + 1 {
		fields = append(fields, "c"+strconv.Itoa(i), parquet.Optional(parquet.Int(32)))
	}
	_, err := columnsOfFile(t, fileOf(t, fields...))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than the 512")

	_, err = columnsOfFile(t, fileOf(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declares no columns")
}

// TestColumns_ReadsTheLegacyShapes pins the two-level LIST forms and a
// converted-type-only file, which older writers produce.
func TestColumns_ReadsTheLegacyShapes(t *testing.T) {
	els := []format.SchemaElement{
		{Name: "schema", NumChildren: nullOf[int32](4)},
		// optional group a (LIST) { repeated int32 element; }
		{Name: "a", RepetitionType: nullOf(format.Optional), NumChildren: nullOf[int32](1), ConvertedType: nullOf(deprecated.List)},
		{Name: "element", RepetitionType: nullOf(format.Repeated), Type: nullOf(format.Int32)},
		// optional group b (LIST) { repeated group array { optional binary s (UTF8); } }
		{Name: "b", RepetitionType: nullOf(format.Optional), NumChildren: nullOf[int32](1), ConvertedType: nullOf(deprecated.List)},
		{Name: "array", RepetitionType: nullOf(format.Repeated), NumChildren: nullOf[int32](1)},
		{Name: "s", RepetitionType: nullOf(format.Optional), Type: nullOf(format.ByteArray), ConvertedType: nullOf(deprecated.UTF8)},
		// optional int64 c (TIMESTAMP_MILLIS)
		{Name: "c", RepetitionType: nullOf(format.Optional), Type: nullOf(format.Int64), ConvertedType: nullOf(deprecated.TimestampMillis)},
		// optional fixed d (DECIMAL(9,2)) via converted type
		{
			Name: "d", RepetitionType: nullOf(format.Optional), Type: nullOf(format.Int32), ConvertedType: nullOf(deprecated.Decimal),
			Scale: nullOf[int32](2), Precision: nullOf[int32](9),
		},
	}
	root, err := buildTree(els)
	require.NoError(t, err)
	cols, err := columnsOf(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"a ARRAY(INTEGER)", `b ARRAY(ROW("s" VARCHAR))`, "c TIMESTAMP(6)", "d DECIMAL(9,2)"},
		declared(cols))

	_, err = buildTree(els[:3])
	require.Error(t, err, "a schema that lists fewer elements than it describes")
	_, err = buildTree(nil)
	require.Error(t, err)
}

func nullOf[T any](v T) thrift.Null[T] { return thrift.New(v) }

func TestWrite_RoundTripsThroughColumns(t *testing.T) {
	parse := func(s string) tabletype.Type {
		typ, err := tabletype.Parse(s)
		require.NoError(t, err)
		return typ
	}
	cols := []tabletype.Column{
		{Name: "Id", Type: parse("bigint")},
		{Name: "amount", Type: parse("decimal(12,2)")},
		{Name: "wide", Type: parse("decimal(38,4)")},
		{Name: "at", Type: parse("timestamp(6)")},
		{Name: "tz", Type: parse("timestamp(3) with time zone")},
		{Name: "u", Type: parse("uuid")},
		{Name: "tags", Type: parse("array(varchar)")},
		{Name: "attrs", Type: parse("map(integer, double)")},
		{Name: "r", Type: parse(`row(a bigint, "b c" row(x real))`)},
		{Name: "flag", Type: parse("boolean")},
		{Name: "day", Type: parse("date")},
		{Name: "raw", Type: parse("varbinary")},
		{Name: "small", Type: parse("smallint")},
	}
	at := time.Date(2024, 5, 1, 12, 34, 56, 789123000, time.UTC)
	rows := [][]any{
		{
			int64(1), "12.34", "-12345678901234567890123456789012.3456", at, at, "6f1c2e8a-6d1e-4c5b-9a3e-0e5f0e5f0e5f",
			[]any{"a", nil},
			map[string]any{"2": json.Number("2.5"), "1": nil},
			[]any{json.Number("7"), []any{json.Number("1.5")}},
			true, "2024-05-01", "AAH/", json.Number("300"),
		},
		make([]any, len(cols)),
		{
			nil, "0", "0", "2024-05-01 12:34:56.789123", "2024-05-01 12:34:56.789 UTC", nil,
			[]any{},
			map[string]any{},
			&tabletype.Object{Keys: []string{"B C", "A"}, Values: []any{nil, json.Number("9")}}, false, at,
			[]byte{1},
			int64(2),
		},
	}
	b, err := Write(cols, rows)
	require.NoError(t, err)

	got, err := columnsOfFile(t, b)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"id BIGINT", "amount DECIMAL(12,2)", "wide DECIMAL(38,4)", "at TIMESTAMP(6)", "tz TIMESTAMP(6)", "u VARCHAR",
		"tags ARRAY(VARCHAR)", "attrs MAP(INTEGER, DOUBLE)", `r ROW("a" BIGINT, "b c" ROW("x" REAL))`, "flag BOOLEAN",
		"day DATE", "raw VARBINARY", "small SMALLINT",
	}, declared(got), "the file declares, in order, what it was written as")

	f, err := parquet.OpenFile(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	assert.Equal(t, int64(3), f.NumRows())
	read := make([]parquet.Row, 3)
	n, _ := parquet.NewReader(bytes.NewReader(b)).ReadRows(read)
	require.Equal(t, 3, n)
	first := valuesByColumn(read[0])
	assert.Equal(t, int64(1), first[0][0].Int64())
	assert.Equal(t, int64(1234), first[1][0].Int64(), "12.34 at scale 2")
	assert.Equal(t, at.UnixMicro(), first[3][0].Int64())
	assert.Equal(t, "6f1c2e8a-6d1e-4c5b-9a3e-0e5f0e5f0e5f", string(first[5][0].ByteArray()))
	assert.Equal(t, []byte{0, 1, 0xff}, first[13][0].ByteArray(), "a nested varbinary arrives as base64")
	third := valuesByColumn(read[2])
	assert.Equal(t, at.UnixMicro(), third[3][0].Int64(), "a nested timestamp arrives as Trino's text")
	assert.Equal(t, at.Truncate(time.Millisecond).UnixMicro(), third[4][0].Int64())
	assert.True(t, read[1][0].IsNull())
}

func valuesByColumn(row parquet.Row) map[int][]parquet.Value {
	out := map[int][]parquet.Value{}
	for _, v := range row {
		out[v.Column()] = append(out[v.Column()], v)
	}
	return out
}

func TestWrite_RefusesAValueItsTypeCannotHold(t *testing.T) {
	tests := []struct {
		typ  string
		v    any
		want string
	}{
		{"decimal(5,2)", "1.234", "more digits after the point"},
		{"decimal(5,2)", "1234.5", "has more digits than DECIMAL(5,2)"},
		{"integer", int64(1) << 40, "does not fit INTEGER"},
		{"date", time.Date(9999999, 1, 1, 0, 0, 0, 0, time.UTC), "does not fit DATE"},
		{"decimal(5,2)", "abc", "is not a decimal"},
		// big.Rat reads these as numbers; a DECIMAL column's value is digits.
		{"decimal(5,2)", "0x10", "is not a decimal"},
		{"decimal(5,2)", "1_0.00", "is not a decimal"},
		{"decimal(5,2)", "1/3", "is not a decimal"},
		{"tinyint", int64(300), "does not fit TINYINT"},
		{"smallint", int64(70000), "does not fit SMALLINT"},
		{"real", 1e300, "does not fit REAL"},
		{"bigint", "x", "invalid syntax"},
		{"boolean", "true", "cannot be written as BOOLEAN"},
		{"double", []any{}, "is not a number"},
		{"date", "05/01/2024", "is not a date"},
		{"timestamp(6)", "yesterday", "is not a timestamp"},
		{"varbinary", 7, "is not binary"},
		{"array(bigint)", "x", "expected a list"},
		{"map(varchar, bigint)", []any{}, "expected a map"},
		{"row(a bigint)", "x", "expected a row"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			typ, err := tabletype.Parse(tt.typ)
			require.NoError(t, err)
			_, err = Write([]tabletype.Column{{Name: "c", Type: typ}}, [][]any{{tt.v}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.True(t, strings.HasPrefix(err.Error(), `row 1, column "c"`), err.Error())
		})
	}
}

func TestLeafValue_ReadsEveryFormAValueArrivesIn(t *testing.T) {
	dbl := tabletype.Scalar(tabletype.Double)
	for in, want := range map[any]float64{
		1.5: 1.5, float32(2.5): 2.5, int64(3): 3, json.Number("4.5"): 4.5, "Infinity": math.Inf(1), "-Infinity": math.Inf(-1),
	} {
		v, err := leafValue(dbl, in)
		require.NoError(t, err, in)
		assert.Equal(t, want, v.Double())
	}
	v, err := leafValue(dbl, "NaN")
	require.NoError(t, err)
	assert.True(t, math.IsNaN(v.Double()))

	bi := tabletype.Scalar(tabletype.Bigint)
	for _, in := range []any{int64(5), 5, int32(5), json.Number("5"), "5", float64(5)} {
		v, err := leafValue(bi, in)
		require.NoError(t, err, in)
		assert.Equal(t, int64(5), v.Int64())
	}
	_, err = leafValue(bi, 5.5)
	assert.Error(t, err)

	text := tabletype.Type{Kind: tabletype.Text}
	for in, want := range map[any]string{
		"s": "s", json.Number("1.50"): "1.50", true: "true", int64(7): "7", 2.5: "2.5",
		time.Date(0, 1, 1, 13, 4, 5, 120000000, time.UTC): "13:04:05.12",
	} {
		v, err := leafValue(text, in)
		require.NoError(t, err)
		assert.Equal(t, want, string(v.ByteArray()))
	}
	v, err = leafValue(text, []any{json.Number("1"), &tabletype.Object{Keys: []string{"z", "a"}, Values: []any{true, nil}}})
	require.NoError(t, err)
	assert.Equal(t, `[1,{"z":true,"a":null}]`, string(v.ByteArray()), "a nested value is its JSON, keys in order")
	v, err = leafValue(text, []byte("b"))
	require.NoError(t, err)
	assert.Equal(t, "b", string(v.ByteArray()))
}

func TestParseTrinoTimestamp_ReadsEveryZoneForm(t *testing.T) {
	want := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	for _, s := range []string{
		"2024-05-01 10:00:00.000 UTC", "2024-05-01 12:00:00.000 +02:00", "2024-05-01 06:00:00 America/New_York",
	} {
		got, err := parseTrinoTimestamp(s)
		require.NoError(t, err, s)
		assert.True(t, got.Equal(want), "%s -> %s", s, got)
	}
}

func TestTwosComplement(t *testing.T) {
	neg := twosComplement(bigFrom(-1), 2)
	assert.Equal(t, []byte{0xff, 0xff}, neg)
	assert.Equal(t, []byte{0x01, 0x00}, twosComplement(bigFrom(256), 2))
	assert.Equal(t, 16, decimalBytes(38))
	assert.Equal(t, 9, decimalBytes(19))
}

func bigFrom(n int64) *big.Int { return big.NewInt(n) }

// TestOrderedGroup_IsAGroup pins the parts of the Node contract parquet-go
// asks a group for beyond its fields.
func TestOrderedGroup_IsAGroup(t *testing.T) {
	g := group("a", parquet.String())
	assert.Equal(t, "group", g.String())
	assert.True(t, g.Required())
	assert.False(t, g.Optional())
	assert.False(t, g.Repeated())
	assert.False(t, g.Leaf())
	assert.Nil(t, g.Encoding())
	assert.Nil(t, g.Compression())
	assert.NotNil(t, g.GoType())
	assert.Equal(t, 0, g.ID())
	assert.False(t, g.Fields()[0].Value(reflect.Value{}).IsValid())
}

func TestFieldValue_ReadsEveryFormARowArrivesIn(t *testing.T) {
	v, err := fieldValue(map[string]any{"Name": "x"}, 0, "name")
	require.NoError(t, err)
	assert.Equal(t, "x", v)
	v, err = fieldValue(map[string]any{"other": 1}, 0, "name")
	require.NoError(t, err)
	assert.Nil(t, v)
	v, err = fieldValue([]any{"only"}, 3, "name")
	require.NoError(t, err)
	assert.Nil(t, v, "a positional row shorter than the type has nulls after it")
	v, err = fieldValue(&tabletype.Object{Keys: []string{"a"}, Values: []any{1}}, 0, "missing")
	require.NoError(t, err)
	assert.Nil(t, v)
}

// TestWrite_RefusesANameNoTableCanDeclare: Columns refuses a column by its
// name as readily as by its type, so the writer refuses the same names before
// a byte is written. Without this an export succeeds, stores its file, and the
// registration the export was made for refuses it.
func TestWrite_RefusesANameNoTableCanDeclare(t *testing.T) {
	tests := []struct {
		name string
		cols []tabletype.Column
		want string
	}{
		{"a comma", []tabletype.Column{col(t, "a,b", "bigint")}, "holds a comma"},
		{"non-ASCII", []tabletype.Column{col(t, "caf\u00e9", "bigint")}, "outside ASCII"},
		{"edged with space", []tabletype.Column{col(t, " id", "bigint")}, "begins or ends with whitespace"},
		{"empty", []tabletype.Column{col(t, "", "bigint")}, "a column needs a name"},
		{
			"two one apart by case",
			[]tabletype.Column{col(t, "Id", "bigint"), col(t, "id", "varchar")},
			`the columns "Id" and "id"`,
		},
		{"a nested name", []tabletype.Column{col(t, "c", `row("a-b" bigint)`)}, `the nested key "a-b"`},
		// The parser lowercases a ROW field name, because Trino reports a
		// column's type uppercased whole, so both names arrive folded and the
		// refusal names them as it has them.
		{
			"two fields one apart by case",
			[]tabletype.Column{col(t, "c", `row("A" bigint, "a" varchar)`)},
			`the fields "a" and "a" are one field`,
		},
		{"a nested name inside a list", []tabletype.Column{col(t, "c", `array(row("a-b" bigint))`)}, `"a-b"`},
		{"a nested name inside a map", []tabletype.Column{col(t, "c", `map(varchar, row("a-b" bigint))`)}, `"a-b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Write(tt.cols, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// col parses a column type for the tests above.
func col(t *testing.T, name, typ string) tabletype.Column {
	t.Helper()
	parsed, err := tabletype.Parse(typ)
	require.NoError(t, err)
	return tabletype.Column{Name: name, Type: parsed}
}

// TestWrite_AZonedTimeKeepsItsOffset: a TIME WITH TIME ZONE is written as text
// because Parquet has no type that reads it back, and the text has to carry
// the offset the column exists for -- the CSV and JSON exports of the same
// query do.
func TestWrite_AZonedTimeKeepsItsOffset(t *testing.T) {
	zoned, err := tabletype.Parse("time(3) with time zone")
	require.NoError(t, err)
	plain, err := tabletype.Parse("time(3)")
	require.NoError(t, err)
	at := time.Date(1, 1, 1, 12, 0, 0, 0, time.FixedZone("", 2*60*60))

	b, err := Write([]tabletype.Column{{Name: "z", Type: zoned}, {Name: "p", Type: plain}}, [][]any{{at, at}})
	require.NoError(t, err)
	read := make([]parquet.Row, 1)
	n, _ := parquet.NewReader(bytes.NewReader(b)).ReadRows(read)
	require.Equal(t, 1, n)
	values := valuesByColumn(read[0])
	assert.Equal(t, "12:00:00+02:00", string(values[0][0].ByteArray()))
	assert.Equal(t, "12:00:00", string(values[1][0].ByteArray()), "a TIME carries no zone")
}

func TestWrite_MapKeysAndTimestampUnits(t *testing.T) {
	keyed, err := tabletype.Parse("map(integer, varchar)")
	require.NoError(t, err)
	_, err = Write([]tabletype.Column{{Name: "m", Type: keyed}}, [][]any{{map[string]any{"x": "no"}}})
	require.Error(t, err, "a map key that is not its key type is refused")
	assert.Contains(t, err.Error(), `the map key "x"`)

	nested := tabletype.MapOf(tabletype.ArrayOf(tabletype.Scalar(tabletype.Bigint)), tabletype.Scalar(tabletype.Bigint))
	_, err = Write([]tabletype.Column{{Name: "m", Type: nested}}, nil)
	require.Error(t, err, "a map keyed by a list cannot be written")

	_, err = Write([]tabletype.Column{{Name: "d", Type: tabletype.DecimalOf(40, 0)}}, nil)
	require.Error(t, err, "a DECIMAL wider than 38 digits cannot be written")

	ts3, err := tabletype.Parse("timestamp(3)")
	require.NoError(t, err)
	at := time.Date(2024, 5, 1, 12, 34, 56, 789000000, time.UTC)
	b, err := Write([]tabletype.Column{{Name: "t", Type: ts3}}, [][]any{{at}})
	require.NoError(t, err)
	read := make([]parquet.Row, 1)
	n, _ := parquet.NewReader(bytes.NewReader(b)).ReadRows(read)
	require.Equal(t, 1, n)
	assert.Equal(t, at.UnixMilli(), read[0][0].Int64(), "a TIMESTAMP(3) is written in milliseconds")

	_, err = leafValue(tabletype.Scalar(tabletype.Timestamp), 7)
	assert.Error(t, err)
}

func TestLeafType_RefusesWhatNoDeclarationReads(t *testing.T) {
	for name, el := range map[string]format.SchemaElement{
		"a BSON document":           {Type: nullOf(format.ByteArray), LogicalType: format.LogicalType{Value: &format.BsonType{}}},
		"a DECIMAL too wide":        {Type: nullOf(format.FixedLenByteArray), LogicalType: format.LogicalType{Value: &format.DecimalType{Precision: 40}}},
		"an unknown converted type": {Type: nullOf(format.Int32), ConvertedType: nullOf(deprecated.ConvertedType(99))},
	} {
		_, err := leafType(el, "c")
		assert.Error(t, err, name)
	}
	assert.Equal(t, "converted type 99", convertedName(99))
	assert.Equal(t, "INTERVAL", convertedName(deprecated.Interval))
}

func TestMapType_RefusesAMalformedGroup(t *testing.T) {
	str := format.SchemaElement{Name: "k", Type: nullOf(format.ByteArray), RepetitionType: nullOf(format.Required)}
	for name, n := range map[string]*node{
		"no key_value group": {el: format.SchemaElement{Name: "m"}},
		"a key that is a group": {el: format.SchemaElement{Name: "m"}, children: []*node{{
			el:       format.SchemaElement{Name: "key_value"},
			children: []*node{{el: format.SchemaElement{Name: "k"}, children: []*node{{el: str}}}, {el: str}},
		}}},
		"a key of a refused type": {el: format.SchemaElement{Name: "m"}, children: []*node{{
			el: format.SchemaElement{Name: "key_value"},
			children: []*node{{el: format.SchemaElement{
				Name: "k", Type: nullOf(format.Int64),
				LogicalType: format.LogicalType{Value: &format.TimeType{}},
			}}, {el: str}},
		}}},
	} {
		_, err := mapType(n, "m")
		assert.Error(t, err, name)
	}
}
