package scriptsql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositional_RewritesPlaceholdersAndKeepsValuesOutOfTheSQL(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		args     []binding
		wantSQL  string
		wantArgs []any
	}{
		{"one", "SELECT * FROM o WHERE id = :id", []binding{bound("id", int64(7))}, "SELECT * FROM o WHERE id = $1", []any{int64(7)}},
		{
			"repeated name reuses its number", "WHERE a = :v OR b = :v AND c = :w",
			[]binding{bound("v", "x"), bound("w", true)},
			"WHERE a = $1 OR b = $1 AND c = $2",
			[]any{"x", true},
		},
		{"injection stays a value", "WHERE r = :r", []binding{bound("r", "x' OR '1'='1")}, "WHERE r = $1", []any{"x' OR '1'='1"}},
		{"list as an array", "WHERE id = ANY(:ids)", []binding{bound("ids", []any{int64(1), int64(2)})}, "WHERE id = ANY($1)", []any{[]any{int64(1), int64(2)}}},
		{"none", "SELECT 1", nil, "SELECT 1", nil},
		{"null", "WHERE x IS NOT DISTINCT FROM :x", []binding{bound("x", nil)}, "WHERE x IS NOT DISTINCT FROM $1", []any{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs, err := Positional(tc.sql, params(t, tc.args...))
			require.NoError(t, err)
			assert.Equal(t, tc.wantSQL, gotSQL)
			assert.Equal(t, tc.wantArgs, gotArgs)
		})
	}
}

func TestPositional_SkipsNonCode(t *testing.T) {
	cases := []struct{ name, sql, want string }{
		{"string literal", "SELECT ':r', :r", "SELECT ':r', $1"},
		{"quoted identifier", `SELECT ":r", :r`, `SELECT ":r", $1`},
		{"line comment", "-- :r\nSELECT :r", "-- :r\nSELECT $1"},
		{"block comment", "/* :r */ SELECT :r", "/* :r */ SELECT $1"},
		{"cast", "SELECT :r::text", "SELECT $1::text"},
		{"dollar quoted", "SELECT $$ :r $$, :r", "SELECT $$ :r $$, $1"},
		{"tagged dollar quoted", "SELECT $fn$ :r $fn$, :r", "SELECT $fn$ :r $fn$, $1"},
		{"positional parameter is not a tag", "SELECT $1, :r", "SELECT $1, $1"},
		{"unterminated dollar quote swallows the rest", "SELECT $$ :r", "SELECT $$ :r"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args []binding
			if tc.name != "unterminated dollar quote swallows the rest" {
				args = []binding{bound("r", "x")}
			}
			got, _, err := Positional(tc.sql, params(t, args...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPositional_Refusals(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		args []binding
		want string
	}{
		{"missing value", "WHERE a = :a", nil, "placeholder :a has no bound value"},
		{"unused value", "SELECT 1", []binding{bound("a", "x")}, `params has "a" but the SQL has no :a placeholder`},
		{"list after IN", "WHERE id IN :ids", []binding{bound("ids", []any{"a"})}, "write = ANY(:ids)"},
		{"list after in (", "WHERE id in (:ids)", []binding{bound("ids", []any{"a"})}, "write = ANY(:ids)"},
		{"table record", "SELECT * FROM :t", []binding{bound("t", map[string]any{"catalog": "c"})}, "registered table is a Trino table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Positional(tc.sql, params(t, tc.args...))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestPrecededByIn(t *testing.T) {
	assert.True(t, precededByIn("x IN ", 5))
	assert.False(t, precededByIn("x JOIN ", 7), "a word ending in in is not IN")
	assert.False(t, precededByIn("x = ", 4))
	assert.True(t, precededByIn("in ", 3))
}
