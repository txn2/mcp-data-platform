package tablejsonl

import (
	"errors"
	"strconv"
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
			for _, c := range got {
				assert.Equal(t, tablecsv.ColumnType, c.Type)
			}
		})
	}
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
		{"a nested object", "{\"a\":{\"b\":1}}\n", "nested object or list"},
		{"a nested list", "{\"a\":[1]}\n", "nested object or list"},
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
