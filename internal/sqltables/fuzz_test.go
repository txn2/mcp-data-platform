package sqltables

import (
	"strings"
	"testing"
)

// FuzzExtract holds the table reader to the statement a trino_query caller
// writes (#2713): every table it names is written in the statement, and the
// kind and summary are bounded whatever the statement is.
func FuzzExtract(f *testing.F) {
	for _, s := range benchStatements {
		f.Add(s)
	}
	f.Add(`SELECT * FROM "quoted"."schema"."t" JOIN TABLE(system.sequence(1, 3)) s ON true`)
	f.Add("SELECT")
	f.Fuzz(func(t *testing.T, sql string) {
		lower := strings.ToLower(sql)
		for _, r := range Extract(sql) {
			if r.Table == "" {
				t.Fatalf("a ref with no table from %q: %#v", sql, r)
			}
			if !strings.Contains(lower, strings.ToLower(strings.Trim(r.Table, `"`))) {
				t.Fatalf("table %q is not in the statement %q", r.Table, sql)
			}
		}
		if s := Summary(sql); len(s) > len(sql)+64 {
			t.Fatalf("summary %q is longer than the statement", s)
		}
		_ = StatementKind(sql)
		_, _ = WriteTarget(sql)
		_ = HasTopLevelOrderBy(sql)
	})
}
