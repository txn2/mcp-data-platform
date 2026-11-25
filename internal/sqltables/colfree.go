package sqltables

import (
	"regexp"
	"strings"
)

// Column-free statement shapes (#2810): a statement that names no column of
// the table it reads, so there is no column name in it to guess.
const (
	// ShapeStar is SELECT * FROM t: its result names every column of t.
	ShapeStar = "star"
	// ShapeCount is SELECT count(*) FROM t: it names no column and its result
	// names none either.
	ShapeCount = "count"
)

// columnFreePattern is SELECT * or SELECT count(*) from one table, with an
// optional alias for either, and at most OFFSET, LIMIT and FETCH after it.
// It is matched against the statement with comments removed and lower case,
// so a WHERE, JOIN, GROUP BY, ORDER BY, HAVING, UNION or a second table does
// not match, and neither does a select list naming anything else.
var columnFreePattern = regexp.MustCompile(`^select\s+(?:all\s+)?(\*|count\s*\(\s*\*\s*\))` +
	`(?:\s+(?:as\s+)?(?P<alias>[a-z_][a-z0-9_]*|"[^"]+"))?` +
	`\s+from\s+((?:"[^"]+"|[a-z_][a-z0-9_$]*)(?:\s*\.\s*(?:"[^"]+"|[a-z_][a-z0-9_$]*)){0,2})` +
	`(?:\s+(?:as\s+)?(?P<tableAlias>[a-z_][a-z0-9_]*|"[^"]+"))?` +
	`(?:\s+offset\s+\d+(?:\s+rows?)?)?` +
	`(?:\s+limit\s+\d+)?` +
	`(?:\s+fetch\s+(?:first|next)\s+\d+\s+rows?\s+only)?` +
	`\s*;?$`)

// clauseWords are the words a column-free statement's alias may not be: each
// opens the clause that would follow the table name instead.
var clauseWords = map[string]bool{"limit": true, "offset": true, "fetch": true, "where": true}

// ColumnFree reports whether a statement reads one table without naming any of
// its columns, and which shape it is: ShapeStar for SELECT * FROM t, ShapeCount
// for SELECT count(*) FROM t, each with at most an alias and LIMIT, OFFSET or
// FETCH. Any other statement, including either shape with a WHERE, a join, a
// grouping or an ordering, is not column-free.
func ColumnFree(sql string) (string, bool) {
	text := strings.TrimSpace(strings.ToLower(withoutComments(sql)))
	m := columnFreePattern.FindStringSubmatch(text)
	if len(m) == 0 || clauseWords[m[columnFreePattern.SubexpIndex("alias")]] || clauseWords[m[columnFreePattern.SubexpIndex("tableAlias")]] {
		return "", false
	}
	if m[1] == "*" {
		return ShapeStar, true
	}
	return ShapeCount, true
}

// withoutComments is sql with every comment replaced by a space; string
// literals and quoted identifiers are kept as they are.
func withoutComments(sql string) string {
	var b strings.Builder
	for i := 0; i < len(sql); i++ {
		skip := skipQuotedOrComment(sql, i)
		if skip <= i {
			_ = b.WriteByte(sql[i])
			continue
		}
		if sql[i] == '\'' || sql[i] == '"' {
			_, _ = b.WriteString(sql[i:skip])
		} else {
			_ = b.WriteByte(' ')
		}
		i = skip - 1
	}
	return b.String()
}
