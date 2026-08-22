package tableregister

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadHeaderColumns covers what a real exported CSV actually looks like:
// a trailing comma, a repeated name, a BOM from a spreadsheet tool, quoted
// fields. A file is what it is, and a table that refuses to exist over an
// ordinary one helps nobody.
func TestReadHeaderColumns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"plain", "store_id,vendor_code\n1,a\n", []string{"store_id", "vendor_code"}},
		{"trailing comma names the column positionally", "a,b,\n", []string{"a", "b", "column_3"}},
		{"repeated names are disambiguated", "id,id,id\n", []string{"id", "id_2", "id_3"}},
		{"case-insensitive collision", "ID,id\n", []string{"ID", "id_2"}},
		{"a BOM is not part of the first name", "\ufeffstore_id,b\n", []string{"store_id", "b"}},
		{"quoted fields with commas", `"last, first",age` + "\n", []string{"last, first", "age"}},
		{"surrounding space is trimmed", " a , b \n", []string{"a", "b"}},
		{"a ragged file still has a header", "a,b\n1\n2,3,4\n", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, err := ReadHeaderColumns([]byte(tt.input))
			require.NoError(t, err)
			got := make([]string, 0, len(cols))
			for _, c := range cols {
				got = append(got, c.Name)
				assert.Equal(t, "VARCHAR", c.Type,
					"Hive CSV admits no other type, so nothing may declare one")
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadHeaderColumns_Refusals(t *testing.T) {
	_, err := ReadHeaderColumns(nil)
	assert.ErrorIs(t, err, ErrEmptyHeader, "an empty file has no header")

	// encoding/csv skips a leading blank line, so the first non-blank line is
	// the header, which is what a file written with a trailing newline before
	// its content means.
	cols, err := ReadHeaderColumns([]byte("\nstore_id,vendor\n1,a\n"))
	require.NoError(t, err)
	assert.Equal(t, "store_id", cols[0].Name)

	// A line of nothing but separators is not a header of unnamed columns.
	_, err = ReadHeaderColumns([]byte(",,\n1,2,3\n"))
	assert.ErrorIs(t, err, ErrEmptyHeader)

	// A first line this wide is a line of data far more often than a header.
	wide := strings.Repeat("a,", maxColumns+1)
	_, err = ReadHeaderColumns([]byte(wide + "\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "more than the 512")
}

// TestQuoteIdentifier is the guard on the DDL: a column name comes from the
// first line of a file somebody uploaded, and Trino has no parameter binding
// for identifiers, so the quote-doubling is what closes the statement.
func TestQuoteIdentifier(t *testing.T) {
	assert.Equal(t, `"store_id"`, QuoteIdentifier("store_id"))
	assert.Equal(t, `"a""b"`, QuoteIdentifier(`a"b`))
	assert.Equal(t, `"x"" , format = 'CSV') --"`, QuoteIdentifier(`x" , format = 'CSV') --`))
	assert.Equal(t, `""`, QuoteIdentifier(""))
}

func TestQuoteLiteral(t *testing.T) {
	assert.Equal(t, `'s3://b/d/'`, QuoteLiteral("s3://b/d/"))
	assert.Equal(t, `'it''s'`, QuoteLiteral("it's"))
}

func TestSlugifyTableName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"vendor-keys.csv", "vendor_keys"},
		{"Q3 Revenue.CSV", "q3_revenue"},
		{"  spaced  out  ", "spaced_out"},
		{"2026-report", "t_2026_report"},
		{"...", ""},
		{"", ""},
		{".hidden", ".hidden"[1:]}, // the leading dot is not an extension
		{strings.Repeat("a", 200), strings.Repeat("a", maxTableNameLength)},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, SlugifyTableName(tt.in), "input %q", tt.in)
	}
}

// TestPrefixedTableName pins that the prefix is applied once. A person who
// names their table after their own persona should not get analyst_analyst_x.
func TestPrefixedTableName(t *testing.T) {
	assert.Equal(t, "analyst_vendors", PrefixedTableName("analyst", "vendors"))
	assert.Equal(t, "analyst_vendors", PrefixedTableName("analyst", "analyst_vendors"))
	assert.Equal(t, "vendors", PrefixedTableName("", "vendors"),
		"a caller with no persona gets the bare slug rather than a leading underscore")
	assert.Equal(t, "data_engineer_vendors", PrefixedTableName("data-engineer", "vendors"))
}

func TestDirectoryOfAndLocationURI(t *testing.T) {
	assert.Equal(t, "a/b/", DirectoryOf("a/b/content.csv"))
	assert.Empty(t, DirectoryOf("content.csv"), "a key with no directory can hold no table")
	assert.Equal(t, "s3://bucket/a/b/", LocationURI("bucket", "a/b/"))
}

// TestIsStale is the whole revision story in one assertion set.
func TestIsStale(t *testing.T) {
	reg := Registration{Location: "s3://b/d/v1/"}

	assert.False(t, reg.IsStale("b", "d/v1/content.csv"), "the head is still in the registered directory")
	assert.True(t, reg.IsStale("b", "d/v2/content.csv"), "a new revision moved the head")
	assert.True(t, reg.IsStale("other", "d/v1/content.csv"), "a different bucket is a different location")
	assert.True(t, reg.IsStale("b", "content.csv"), "a head with no directory can match nothing")
}

func TestQualifiedName(t *testing.T) {
	reg := Registration{Catalog: "scratch", Schema: "uploads", Table: "analyst_keys"}
	assert.Equal(t, "scratch.uploads.analyst_keys", reg.QualifiedName())
}

// TestBuildDDL pins the statements and their order. CREATE SCHEMA is first and
// idempotent because the first registration on a connection has to make it;
// DROP appears only when replacing, because an unconditional one would let a
// name collision quietly remove somebody else's table.
func TestBuildDDL(t *testing.T) {
	reg := Registration{
		Catalog: "scratch", Schema: "uploads", Table: "analyst_keys",
		Location: "s3://b/d/",
		Columns:  []Column{{Name: "id", Type: "VARCHAR"}},
	}

	assert.Equal(t, []string{
		`CREATE SCHEMA IF NOT EXISTS "scratch"."uploads"`,
		`CREATE TABLE "scratch"."uploads"."analyst_keys" ("id" VARCHAR) ` +
			`WITH (external_location = 's3://b/d/', format = 'CSV', skip_header_line_count = 1)`,
	}, BuildDDL(reg, false))

	replacing := BuildDDL(reg, true)
	require.Len(t, replacing, 3)
	assert.Equal(t, `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_keys"`, replacing[1])
}

// TestSampleJoinSQL: every column is VARCHAR, so the obvious join fails with a
// type error that explains nothing. This is the explanation.
func TestSampleJoinSQL(t *testing.T) {
	reg := Registration{
		Catalog: "scratch", Schema: "uploads", Table: "analyst_keys",
		Columns: []Column{{Name: "store_id", Type: "VARCHAR"}},
	}
	sample := SampleJoinSQL(reg)
	assert.Contains(t, sample, "SELECT * FROM scratch.uploads.analyst_keys")
	assert.Contains(t, sample, `CAST(t."store_id" AS BIGINT)`)

	assert.Empty(t, SampleJoinSQL(Registration{}), "a table with no columns has no join to show")
}

// TestIsCSV covers the fallback: the stored content type decides, and a
// generic or absent one falls back to the key's extension, which is what a
// record written before detection carries.
func TestIsCSV(t *testing.T) {
	assert.True(t, isCSV("text/csv", "x.bin"))
	assert.True(t, isCSV("text/csv; charset=utf-8", "x.bin"))
	assert.True(t, isCSV("application/csv", "x.bin"))
	assert.False(t, isCSV("text/html", "x.csv"), "a declared specific type is believed")
	assert.True(t, isCSV("application/octet-stream", "x.csv"))
	assert.True(t, isCSV("text/plain", "x.CSV"))
	assert.True(t, isCSV("", "x.csv"))
	assert.False(t, isCSV("", "x.txt"))
}

func TestJoinAnd(t *testing.T) {
	assert.Empty(t, joinAnd(nil))
	assert.Equal(t, "a", joinAnd([]string{"a"}))
	assert.Equal(t, "a and b", joinAnd([]string{"a", "b"}))
	assert.Equal(t, "a, b, and c", joinAnd([]string{"a", "b", "c"}))
}

func TestFileNameOf(t *testing.T) {
	assert.Equal(t, "content.csv", fileNameOf("a/b/content.csv"))
	assert.Equal(t, "content.csv", fileNameOf("content.csv"))
}
