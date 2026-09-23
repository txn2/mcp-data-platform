package tabletype

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_ReadsTheTypesTrinoReports(t *testing.T) {
	tests := []struct {
		text string
		want string
		kind Kind
	}{
		{"boolean", "BOOLEAN", Boolean},
		{"TINYINT", "TINYINT", Tinyint},
		{"smallint", "SMALLINT", Smallint},
		{"integer", "INTEGER", Integer},
		{"bigint", "BIGINT", Bigint},
		{"real", "REAL", Real},
		{"double", "DOUBLE", Double},
		{"decimal(12,2)", "DECIMAL(12,2)", Decimal},
		{"varchar", "VARCHAR", Varchar},
		{"varchar(10)", "VARCHAR", Varchar},
		{"varbinary", "VARBINARY", Varbinary},
		{"date", "DATE", Date},
		{"timestamp(3)", "TIMESTAMP(6)", Timestamp},
		{"timestamp(6) with time zone", "TIMESTAMP WITH TIME ZONE", TimestampTZ},
		{"char(3)", "VARCHAR", Text},
		{"time(3)", "VARCHAR", Text},
		{"time(3) with time zone", "VARCHAR", Text},
		{"uuid", "VARCHAR", Text},
		{"json", "VARCHAR", Text},
		{"interval day to second", "VARCHAR", Text},
		{"array(bigint)", "ARRAY(BIGINT)", Array},
		{"map(varchar, array(double))", "MAP(VARCHAR, ARRAY(DOUBLE))", Map},
		{`row(a bigint, "b ""c" varchar(3))`, `ROW("a" BIGINT, "b ""c" VARCHAR)`, Row},
		{"row(bigint, decimal(10,2))", `ROW("field0" BIGINT, "field1" DECIMAL(10,2))`, Row},
		// A type this parser does not know runs to several words, so reading
		// the first as a field name would name the field after its own type.
		{"row(interval day to second)", `ROW("field0" VARCHAR)`, Row},
		{"row(time with time zone)", `ROW("field0" VARCHAR)`, Row},
		{"row(a bigint, interval day to second)", `ROW("a" BIGINT, "field1" VARCHAR)`, Row},
		{`row("t" time(3) with time zone)`, `ROW("t" VARCHAR)`, Row},
		{"array(row(t timestamp(6), n row(x integer)))", `ARRAY(ROW("t" TIMESTAMP(6), "n" ROW("x" INTEGER)))`, Array},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := Parse(tt.text)
			require.NoError(t, err)
			assert.Equal(t, tt.kind, got.Kind)
			assert.Equal(t, tt.want, got.SQL())
		})
	}
}

func TestParse_KeepsWhatTheWriterNeeds(t *testing.T) {
	ts, err := Parse("timestamp(3)")
	require.NoError(t, err)
	assert.Equal(t, 3, ts.Precision)

	text, err := Parse("time(3) with time zone")
	require.NoError(t, err)
	assert.Equal(t, "time(3) with time zone", text.Source)

	bare, err := Parse("DECIMAL")
	require.NoError(t, err)
	assert.Equal(t, "DECIMAL(12,2)", bare.WithPrecision(12, 2).SQL())
	assert.Equal(t, "DECIMAL(5,1)", DecimalOf(5, 1).WithPrecision(12, 2).SQL(), "a type's own numbers win")
	assert.Equal(t, 6, Scalar(Timestamp).WithPrecision(6, 0).Precision)
	assert.Equal(t, 0, Scalar(Bigint).WithPrecision(6, 0).Precision)
}

func TestParse_RefusesWhatIsNotAType(t *testing.T) {
	for _, text := range []string{
		"", "array(", "map(bigint)", "decimal(x)", "row(a bigint", `row("a bigint)`, "bigint extra",
		strings.Repeat("array(", 40) + "bigint" + strings.Repeat(")", 40),
	} {
		_, err := Parse(text)
		assert.Error(t, err, "%q", text)
	}
}

func TestCheckFieldName(t *testing.T) {
	for _, ok := range []string{"a", "A_b", "d.e", "a b", "a$b", "x1"} {
		assert.NoError(t, CheckFieldName(ok), ok)
	}
	for _, tc := range []struct{ name, want string }{
		{"b-c", "holds '-'"},
		{"a:b", "holds ':'"},
		{`q"t`, `holds '"'`},
		{"", "a key is empty"},
		{"a,b", "holds a comma"},
		{" lead", "whitespace"},
		{"café", "outside ASCII"},
	} {
		err := CheckFieldName(tc.name)
		require.Error(t, err, tc.name)
		assert.Contains(t, err.Error(), tc.want, tc.name)
	}
}

func TestDecodeJSON_KeepsKeyOrderAndNumbers(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"z":1,"a":{"y":[1.5,null,"s"],"b":true},"z":2}`))
	require.NoError(t, err)
	obj, ok := v.(*Object)
	require.True(t, ok)
	assert.Equal(t, []string{"z", "a", "z"}, obj.Keys, "a repeated key is kept so it can be refused")
	assert.Equal(t, json.Number("1"), obj.Values[0])
	inner, ok := obj.Values[1].(*Object)
	require.True(t, ok)
	assert.Equal(t, []string{"y", "b"}, inner.Keys)
	got, found := inner.Get("y")
	assert.True(t, found)
	assert.Equal(t, []any{json.Number("1.5"), nil, "s"}, got)
	_, found = inner.Get("missing")
	assert.False(t, found)

	_, err = DecodeJSON([]byte(`{"a":1}{"a":2}`))
	assert.True(t, IsTrailing(err))
	_, err = DecodeJSON([]byte(strings.Repeat("[", 40) + strings.Repeat("]", 40)))
	assert.ErrorIs(t, err, ErrTooDeep)
	_, err = DecodeJSON([]byte(`{"a":`))
	assert.Error(t, err)
}

func observe(t *testing.T, lines ...string) ([]Column, error) {
	t.Helper()
	in := NewInferrer()
	for _, line := range lines {
		v, err := DecodeJSON([]byte(line))
		require.NoError(t, err)
		obj, ok := v.(*Object)
		require.True(t, ok, line)
		if err := in.Observe(obj.Keys, obj.Values); err != nil {
			return nil, err
		}
	}
	return in.Columns()
}

func TestInferrer_Rules(t *testing.T) {
	cols, err := observe(t,
		`{"b":true,"i":1,"f":1,"s":"x","m":1,"n":null,"big":99999999999999999999,"o":{"k":1},"l":[1,2.5],`+
			`"huge":1e999}`,
		`{"b":false,"i":-2,"f":2.5,"s":"y","m":"x","big":1,"o":{"K":2,"j":"z"},"l":[],"late":{"q":[true]},`+
			`"huge":1.5}`,
	)
	require.NoError(t, err)
	got := map[string]string{}
	order := make([]string, 0, len(cols))
	for _, c := range cols {
		got[c.Name] = c.Type.SQL()
		order = append(order, c.Name)
	}
	assert.Equal(t, []string{"b", "i", "f", "s", "m", "n", "big", "o", "l", "huge", "late"}, order)
	assert.Equal(t, map[string]string{
		"b": "BOOLEAN", "i": "BIGINT", "f": "DOUBLE", "s": "VARCHAR", "m": "VARCHAR", "n": "VARCHAR",
		"big": "VARCHAR", "o": `ROW("k" BIGINT, "j" VARCHAR)`, "l": "ARRAY(DOUBLE)", "late": `ROW("q" ARRAY(BOOLEAN))`,
		// A fractional number outside float64 keeps its digits as text, the
		// same rule an integer outside int64 follows: written as a DOUBLE it
		// would be stored as an infinity, and the export would fail on it.
		"huge": "VARCHAR",
	}, got)
}

func TestInferrer_RefusesAKeyThatCannotBeOneType(t *testing.T) {
	tests := []struct {
		lines []string
		want  string
	}{
		{[]string{`{"a":{"b":1}}`, `{"a":[1]}`}, `"a" is a list here and was an object`},
		{[]string{`{"a":1}`, `{"a":[1]}`}, `"a" is a list here and was a scalar`},
		{[]string{`{"a":[{"b":1}]}`, `{"a":[{"b":{"c":1}}]}`}, `"a[].b" is an object here and was a scalar`},
		{[]string{`{"a":{}}`}, "an object with no keys"},
		{[]string{`{"a":{"x":1,"X":2}}`}, `the keys "x" and "X" are one field`},
		{[]string{`{"a":{"b:c":1}}`}, `the nested key "b:c"`},
	}
	for _, tt := range tests {
		_, err := observe(t, tt.lines...)
		require.Error(t, err, tt.lines)
		assert.Contains(t, err.Error(), tt.want)
	}
}

func TestInferrer_AColumnOfNullsIsVarchar(t *testing.T) {
	cols, err := observe(t, `{"a":null}`, `{"a":null,"b":[]}`)
	require.NoError(t, err)
	assert.Equal(t, "VARCHAR", cols[0].Type.SQL())
	assert.Equal(t, "ARRAY(VARCHAR)", cols[1].Type.SQL())
}

func TestParse_FoldsRowFieldNames(t *testing.T) {
	got, err := Parse(`ARRAY(ROW(A BIGINT, "B C" VARCHAR))`)
	require.NoError(t, err)
	assert.Equal(t, `ARRAY(ROW("a" BIGINT, "b c" VARCHAR))`, got.SQL())
	assert.True(t, got.Nested())
	assert.False(t, Scalar(Bigint).Nested())
}

func TestInferrer_Len(t *testing.T) {
	in := NewInferrer()
	assert.Equal(t, 0, in.Len())
	require.NoError(t, in.Observe([]string{"a", "b"}, []any{nil, true}))
	require.NoError(t, in.Observe([]string{"b", "c"}, []any{false, nil}))
	assert.Equal(t, 3, in.Len())
}

func TestParse_RefusesAMalformedMap(t *testing.T) {
	for _, text := range []string{"map", "map(varchar bigint)", "map(varchar, )", "map(,bigint)"} {
		_, err := Parse(text)
		assert.Error(t, err, text)
	}
}
