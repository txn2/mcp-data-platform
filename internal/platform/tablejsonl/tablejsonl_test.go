package tablejsonl

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
)

func names(columns []tablecsv.Column) []string {
	out := make([]string, 0, len(columns))
	for _, c := range columns {
		out = append(out, c.Name)
	}
	return out
}

func TestColumns_ReadsTheShapesTheReaderReads(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"one record per line", "{\"id\":1,\"text\":\"a\"}\n{\"id\":2,\"text\":\"b\"}\n", []string{"id", "text"}},
		{"CRLF line endings", "{\"a\":\"1\"}\r\n{\"a\":\"2\"}\r\n", []string{"a"}},
		{"no final newline", "{\"a\":\"1\"}\n{\"a\":\"2\"}", []string{"a"}},
		{"a byte-order mark", "\ufeff{\"a\":\"1\"}\n", []string{"a"}},
		{
			"keys lowercased, first-seen order, union across records",
			"{\"Order ID\":\"1\",\"Amount\":2}\n{\"note\":null,\"amount\":3}\n",
			[]string{"order id", "amount", "note"},
		},
		{"line breaks and backslashes inside values", "{\"t\":\"a\\nb\\r\\nc\\\\d\"}\n", []string{"t"}},
		{"scalars of every kind", "{\"s\":\"x\",\"n\":1.5,\"b\":true,\"z\":null}\n", []string{"s", "n", "b", "z"}},
		{
			"a key with punctuation the reader matches", "{\"a-b\":1,\"a.b\":2,\"a/b\":3,\"1x\":4,\"q\\\"t\":5,\"x y\":6}\n",
			[]string{"a-b", "a.b", "a/b", "1x", "q\"t", "x y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Columns([]byte(tt.body))
			require.NoError(t, err)
			assert.Equal(t, tt.want, names(got))
		})
	}
}

func types(columns []tablecsv.Column) map[string]string {
	out := make(map[string]string, len(columns))
	for _, c := range columns {
		out[c.Name] = c.Type
	}
	return out
}

// TestColumns_TypesTheColumnsFromEveryRecord pins the inference a registration
// declares (#1833): the rule for each kind of value, across every record.
func TestColumns_TypesTheColumnsFromEveryRecord(t *testing.T) {
	body := `{"i":1,"f":1.5,"b":true,"s":"x","mix":1,"nul":null,"big":123456789012345678901,"e":1e3}
{"i":2,"f":2,"b":false,"s":"y","mix":"z","nul":null,"big":1,"e":2}
{"i":null,"mix":false,"late":7}
`
	got, err := Columns([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, []string{"i", "f", "b", "s", "mix", "nul", "big", "e", "late"}, names(got))
	assert.Equal(t, map[string]string{
		"i": "BIGINT", "f": "DOUBLE", "b": "BOOLEAN", "s": "VARCHAR", "mix": "VARCHAR", "nul": "VARCHAR",
		"big": "VARCHAR", "e": "DOUBLE", "late": "BIGINT",
	}, types(got))
}

// TestColumns_DeclaresNestedValues pins that a consistently shaped object is a
// ROW over the union of its keys and a list an ARRAY of its elements (#1833),
// where the file used to be refused.
func TestColumns_DeclaresNestedValues(t *testing.T) {
	body := `{"r":{"Id":1,"tags":["a"]},"a":[1,2],"deep":[{"x":1}]}
{"r":{"id":2,"note":"n"},"a":[],"deep":[{"x":2.5,"y":true}]}
{"r":null,"a":null,"empty":[]}
`
	got, err := Columns([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"r":     `ROW("id" BIGINT, "tags" ARRAY(VARCHAR), "note" VARCHAR)`,
		"a":     "ARRAY(BIGINT)",
		"deep":  `ARRAY(ROW("x" DOUBLE, "y" BOOLEAN))`,
		"empty": "ARRAY(VARCHAR)",
	}, types(got))
}

func TestColumns_RefusesWhatTheReaderCannotRead(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"a blank line", "{\"a\":1}\n\n{\"a\":2}\n", "line 2 is blank"},
		{"a whitespace-only line", "{\"a\":1}\n   \n{\"a\":2}\n", "line 2 is blank"},
		{"two objects on one line", "{\"a\":1}{\"a\":2}\n", "more than one JSON value"},
		{"a line that is a list", "[1,2]\n", "not one JSON object"},
		{"a line that is null", "null\n", "null rather than an object"},
		{"a line that is not JSON", "{\"a\":1}\nnot json\n", "line 2: the line is not one JSON object"},
		{"a repeated key", "{\"a\":1,\"a\":2}\n", "one column to the reader"},
		{"keys differing only by case", "{\"a\":1,\"A\":2}\n", "one column to the reader"},
		{"an object then a list", "{\"a\":{\"b\":1}}\n{\"a\":[1]}\n", `line 2: the value of "a" is a list here and was an object`},
		{"a scalar then an object", "{\"a\":1}\n{\"a\":{\"b\":1}}\n", `line 2: the value of "a" is an object here and was a scalar`},
		{"a list then a scalar", "{\"a\":[1]}\n{\"a\":\"x\"}\n", `line 2: the value of "a" is a scalar here and was a list`},
		{"conflicting elements", "{\"a\":[[1],{\"b\":1}]}\n", `the value of "a[]" is an object here and was a list`},
		{"an object with no keys", "{\"a\":{}}\n", `"a" is an object with no keys on every line`},
		{"a nested key the metastore cannot store", "{\"a\":{\"b-c\":1}}\n", `the nested key "b-c" holds '-'`},
		{"nested keys differing only by case", "{\"a\":{\"b\":1,\"B\":2}}\n", `in "a", the keys "b" and "B" are one field`},
		{"nesting too deep", "{\"a\":" + strings.Repeat("[", 40) + strings.Repeat("]", 40) + "}\n", "nested more than"},
		{"an empty key", "{\"\":1}\n", "a key is empty"},
		{"a padded key", "{\" a\":1}\n", "begins or ends with whitespace"},
		{"a key with a comma", "{\"a,b\":1}\n", "holds a comma"},
		{"a non-ASCII key", "{\"café\":1}\n", "outside ASCII"},
		{"invalid UTF-8", "{\"a\":\"\xff\"}\n", "not valid UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Columns([]byte(tt.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestColumns_NoRecords(t *testing.T) {
	for _, body := range []string{"", "\ufeff"} {
		_, err := Columns([]byte(body))
		assert.True(t, errors.Is(err, ErrNoRecords), "%q", body)
	}
}

func TestColumns_RefusesMoreColumnsThanATableDeclares(t *testing.T) {
	var b []byte
	b = append(b, '{')
	for i := range maxColumns + 1 {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, []byte(`"c`+strconv.Itoa(i)+`":1`)...)
	}
	b = append(b, '}', '\n')
	_, err := Columns(b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than the 512 keys")
}
