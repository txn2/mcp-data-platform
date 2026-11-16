// Package sqlread reads what a SQL statement a script writes does to tables,
// from its text alone: the tables it writes and by which verb, the tables it
// reads, the schema a table name is in, and whether a name carries a part the
// script computes ({its source}). The script flow graph and an application's
// diagram read statements through it.
package sqlread

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// dmlRe reads the statement verb and target a trino_execute writes with.
var dmlRe = regexp.MustCompile(`(?i)^\s*(INSERT\s+INTO|DELETE\s+FROM|UPDATE|MERGE\s+INTO|CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?|CREATE\s+OR\s+REPLACE\s+VIEW|CREATE\s+VIEW|DROP\s+TABLE(?:\s+IF\s+EXISTS)?|TRUNCATE\s+TABLE|ALTER\s+TABLE|CALL)\s+([^\s(]+)`)

// withRe matches a statement that starts with common table expressions.
var withRe = regexp.MustCompile(`(?i)^\s*WITH\s`)

// cteDMLRe reads each data-modifying statement of a statement that starts with
// WITH (#2615): one a common table expression runs opens with "(", and the
// statement that follows the expressions comes after the last ")". Neither
// position holds a verb otherwise: ON CONFLICT ... DO UPDATE and FOR UPDATE put
// a word before it.
var cteDMLRe = regexp.MustCompile(`(?i)[()]\s*(INSERT\s+INTO|DELETE\s+FROM|UPDATE|MERGE\s+INTO)\s+([^\s(),;]+)`)

// Write is one table a statement writes, with the verb it writes it by.
type Write struct{ Verb, Table string }

// WritesOf is every table a statement writes, in the order it names them: the
// target of a statement that starts with its verb, or, for one that starts with
// WITH, each common table expression's target and the target of the statement
// that follows them.
func WritesOf(sql string) []Write {
	if withRe.MatchString(sql) {
		matches := cteDMLRe.FindAllStringSubmatch(sql, -1)
		if len(matches) == 0 {
			return nil
		}
		out := make([]Write, 0, len(matches))
		for _, m := range matches {
			out = append(out, Write{Verb: strings.ToUpper(strings.Fields(m[1])[0]), Table: m[2]})
		}
		return out
	}
	if m := dmlRe.FindStringSubmatch(sql); m != nil {
		return []Write{{Verb: strings.ToUpper(strings.Fields(m[1])[0]), Table: m[2]}}
	}
	return nil
}

// LastWrite is the write a card names for a statement: the statement's own
// target, which follows any its common table expressions write.
func LastWrite(sql string) (Write, bool) {
	ws := WritesOf(sql)
	if len(ws) == 0 {
		return Write{}, false
	}
	return ws[len(ws)-1], true
}

var (
	holeRe = regexp.MustCompile(`\{[^{}]*\}`)
	holeID = regexp.MustCompile(`zzhole(\d+)zz`)
)

// TablesIn lists the tables a rendered SQL statement reads. A computed part of
// a name is kept as {its source}, so "warehouse.public.{t}" is reported as
// that rather than dropped or guessed.
func TablesIn(sql string) []string {
	var holes []string
	masked := holeRe.ReplaceAllStringFunc(sql, func(m string) string {
		holes = append(holes, m)
		return fmt.Sprintf("zzhole%dzz", len(holes)-1)
	})
	seen := map[string]bool{}
	out := []string{}
	for _, ref := range sqltables.Extract(masked) {
		p := holeID.ReplaceAllStringFunc(ref.FullPath, func(m string) string {
			var i int
			_, _ = fmt.Sscanf(m, "zzhole%dzz", &i)
			if i < len(holes) {
				return holes[i]
			}
			return m
		})
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// HasHole reports whether any name carries a computed part.
func HasHole(names []string) bool {
	for _, n := range names {
		if strings.Contains(n, "{") {
			return true
		}
	}
	return false
}

// SchemaOf is the schema a table name written in SQL is in: its qualifier,
// or public for an unqualified name, which is where an application
// database's search path finds it. A three-part name's middle part is the
// schema.
func SchemaOf(table string) string {
	parts := strings.Split(strings.Trim(table, `"`), ".")
	switch len(parts) {
	case 1:
		return "public"
	case 2:
		return strings.Trim(parts[0], `"`)
	default:
		return strings.Trim(parts[len(parts)-2], `"`)
	}
}
