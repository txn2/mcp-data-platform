package pglex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplit(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		want []string
	}{
		"empty":           {"  ;; ", []string{}},
		"two":             {"SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		"string":          {"SELECT ';'; SELECT 2", []string{"SELECT ';'", "SELECT 2"}},
		"doubled quote":   {"SELECT 'it''s;'", []string{"SELECT 'it''s;'"}},
		"e-string":        {`SELECT E'\';'; SELECT 3`, []string{`SELECT E'\';'`, "SELECT 3"}},
		"identifier":      {`SELECT 1 AS "a;b"`, []string{`SELECT 1 AS "a;b"`}},
		"dollar body":     {"DO $x$ BEGIN; END $x$; SELECT 1", []string{"DO $x$ BEGIN; END $x$", "SELECT 1"}},
		"positional":      {"SELECT $1; SELECT 2", []string{"SELECT $1", "SELECT 2"}},
		"line comment":    {"SELECT 1 -- ;\n; SELECT 2", []string{"SELECT 1 -- ;", "SELECT 2"}},
		"nested comment":  {"SELECT /* a /* ; */ ; */ 1; SELECT 2", []string{"SELECT /* a /* ; */ ; */ 1", "SELECT 2"}},
		"unclosed string": {"SELECT 'x; SELECT 2", []string{"SELECT 'x; SELECT 2"}},
		"unclosed dollar": {"SELECT $a$ x; y", []string{"SELECT $a$ x; y"}},
		"unclosed block":  {"SELECT /* x; y", []string{"SELECT /* x; y"}},
		"unclosed line":   {"SELECT 1 -- x; y", []string{"SELECT 1 -- x; y"}},
		"atomic body": {
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT CASE WHEN true THEN 2 END; END; SELECT 3",
			[]string{"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT CASE WHEN true THEN 2 END; END", "SELECT 3"},
		},
		"atomic after a comment": {
			"CREATE PROCEDURE p() LANGUAGE sql BEGIN /* c */ ATOMIC INSERT INTO t VALUES (1); END; SELECT 1",
			[]string{"CREATE PROCEDURE p() LANGUAGE sql BEGIN /* c */ ATOMIC INSERT INTO t VALUES (1); END", "SELECT 1"},
		},
		"begin is a transaction": {"BEGIN; SELECT 1; COMMIT", []string{"BEGIN", "SELECT 1", "COMMIT"}},
		"case outside a body":    {"SELECT CASE WHEN x THEN 1 END; SELECT 2", []string{"SELECT CASE WHEN x THEN 1 END", "SELECT 2"}},
		"words in identifiers":   {"SELECT end_at, case_id FROM begin_atomic; SELECT 2", []string{"SELECT end_at, case_id FROM begin_atomic", "SELECT 2"}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Split(tc.sql))
		})
	}
}

func TestLexemes(t *testing.T) {
	assert.True(t, IsIdentByte('_'))
	assert.False(t, IsIdentByte(';'))
	assert.True(t, IsEString("e'x'", 1))
	assert.False(t, IsEString("be'x'", 2))
	assert.Equal(t, 0, SkipComment("x", 0))
	assert.Equal(t, 3, SkipString(`'a'`, 0, false))
	assert.Equal(t, 0, SkipDollarQuoted("$1", 0))
	assert.Equal(t, 0, SkipDollarQuoted("$", 0))
}
