package scriptout

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func page(rows ...map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	return out
}

func TestNewSpool_RefusesAFormatWhosePagesDoNotJoin(t *testing.T) {
	for _, format := range []string{"parquet", "json", "xlsx", "markdown"} {
		_, err := NewSpool("pages", format)
		require.Error(t, err, format)
		assert.Contains(t, err.Error(), "csv or jsonl")
	}
}

func TestSpool_JSONLinesJoinsPages(t *testing.T) {
	s, err := NewSpool("pages", "jsonl")
	require.NoError(t, err)
	require.NoError(t, s.Append([]string{"n"}, page(map[string]any{"n": 1}, map[string]any{"n": 2})))
	// A later page may carry a key the first did not: each line is its own
	// object.
	require.NoError(t, s.Append([]string{"n", "extra"}, page(map[string]any{"n": 3, "extra": "x"})))
	data, ident, err := s.Data()
	require.NoError(t, err)
	assert.Equal(t, "{\"n\":1}\n{\"n\":2}\n{\"n\":3,\"extra\":\"x\"}\n", string(data))
	assert.Equal(t, 3, s.Rows())
	assert.Equal(t, len(data), s.Bytes())
	assert.NotEmpty(t, ident.ContentType)
}

func TestSpool_CSVKeepsOneHeader(t *testing.T) {
	s, err := NewSpool("pages", "csv")
	require.NoError(t, err)
	// A column name holding a line break is quoted over two lines; the
	// header is parsed, not cut at the first newline.
	cols := []string{"id", "multi\nline"}
	require.NoError(t, s.Append(cols, page(map[string]any{"id": 1, "multi\nline": "a"})))
	require.NoError(t, s.Append(cols, page(map[string]any{"id": 2, "multi\nline": "b"})))
	require.NoError(t, s.Append(cols, page()), "an empty page adds nothing")
	data, _, err := s.Data()
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "\"multi\nline\""), "one header: %q", data)
	assert.Equal(t, "id,\"multi\nline\"\n1,a\n2,b\n", string(data))
}

func TestSpool_CSVRefusesAColumnTheHeaderLacks(t *testing.T) {
	s, err := NewSpool("pages", "csv")
	require.NoError(t, err)
	require.NoError(t, s.Append([]string{"id"}, page(map[string]any{"id": 1})))
	err = s.Append([]string{"id", "surprise"}, page(map[string]any{"id": 2, "surprise": "x"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "surprise")
	// A page missing a column the header has is written with the cell empty.
	require.NoError(t, s.Append([]string{"id"}, page(map[string]any{"id": 3})))
}

func TestSpool_RefusesToGrowPastTheCeiling(t *testing.T) {
	s, err := NewSpool("pages", "jsonl")
	require.NoError(t, err)
	s.data.Grow(1)
	_, _ = s.data.WriteString(strings.Repeat("x", MaxBytes-4))
	err = s.Append([]string{"v"}, page(map[string]any{"v": "too much"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the")
}

func TestSpool_AnEmptyOutputIsAnEmptyFile(t *testing.T) {
	s, err := NewSpool("pages", "jsonl")
	require.NoError(t, err)
	data, ident, err := s.Data()
	require.NoError(t, err)
	assert.Empty(t, data)
	assert.NotEmpty(t, ident.ContentType, "an empty output still has a type")
}

func TestSpool_RowFailuresAreTheSerializers(t *testing.T) {
	s, err := NewSpool("pages", "jsonl")
	require.NoError(t, err)
	err = s.Append([]string{"v"}, page(map[string]any{"v": string([]byte{0xff})}))
	require.Error(t, err, "a string that is not UTF-8 fails a JSON-lines output")
}

func TestWithoutHeader_RefusesAnUnreadablePage(t *testing.T) {
	_, err := withoutHeader([]byte("\"unterminated"))
	require.Error(t, err)
}

func TestCopiedIsWrittenWholeInItsFormat(t *testing.T) {
	csv := (&Copied{Data: []byte("a\n1\n"), Rows: 1, Format: "csv"}).Spool("out")
	data, ident, err := csv.Data()
	if err != nil || string(data) != "a\n1\n" || ident.ContentType != "text/csv" || ident.Extension != "csv" {
		t.Fatalf("csv = %q %+v %v", data, ident, err)
	}
	if !csv.Copied() || csv.Rows() != 1 {
		t.Errorf("a copied spool reports Copied and its rows: %v %d", csv.Copied(), csv.Rows())
	}
	c := &Copied{Data: []byte("a\t1\n"), Rows: 1, Format: CopyFormatText}
	_, ident, _ = c.Spool("out").Data()
	if ident.ContentType != "text/tab-separated-values" || ident.Extension != "tsv" {
		t.Errorf("COPY text is stored as tsv: %+v", ident)
	}
	if c.String() != "<copied 1 rows>" || c.Type() != "copied" || !bool(c.Truth()) {
		t.Errorf("value methods: %s %s %v", c.String(), c.Type(), c.Truth())
	}
	c.Freeze()
	if _, err := c.Hash(); err == nil {
		t.Error("copied bytes hash")
	}
	if s, _ := NewSpool("x", "csv"); s.Copied() {
		t.Error("an appended output's spool is not copied")
	}
}
