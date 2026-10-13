package sqltables

import (
	"regexp"
	"strings"
)

// writeTargetPattern matches a statement that writes a table from what it
// reads, and captures the table it writes: INSERT INTO, CREATE TABLE ... AS,
// CREATE OR REPLACE TABLE ... AS, MERGE INTO, and CREATE [OR REPLACE] VIEW,
// whose target is a view over its sources. A name may be quoted.
var writeTargetPattern = regexp.MustCompile(`(?is)^\s*(?:` +
	`INSERT\s+INTO|MERGE\s+INTO|` +
	`CREATE\s+(?:OR\s+REPLACE\s+)?(?:TABLE|VIEW|MATERIALIZED\s+VIEW)(?:\s+IF\s+NOT\s+EXISTS)?` +
	`)\s+((?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_]*)(?:\.(?:"[^"]+"|[a-zA-Z_][a-zA-Z0-9_]*)){0,2})`)

// createPattern recognizes the CREATE forms, which write from a source only
// when they carry AS; a CREATE TABLE with a column list and no AS reads
// nothing.
var createPattern = regexp.MustCompile(`(?is)^\s*CREATE\s`)

// asPattern finds the AS that introduces a CREATE's query.
var asPattern = regexp.MustCompile(`(?is)\bAS\s*(?:\(\s*)?(?:SELECT|WITH|TABLE|VALUES)\b`)

// WriteTarget returns the table a statement writes from what it reads, and
// false for any statement that is not such a write. The lineage of a statement
// is an edge from each table Extract names to this one.
func WriteTarget(sql string) (Ref, bool) {
	m := writeTargetPattern.FindStringSubmatch(sql)
	if m == nil {
		return Ref{}, false
	}
	if createPattern.MatchString(sql) && !asPattern.MatchString(sql) {
		return Ref{}, false
	}
	ref := parseTablePath(strings.ReplaceAll(m[1], `"`, ""))
	ref.Source = "TARGET"
	return ref, true
}
