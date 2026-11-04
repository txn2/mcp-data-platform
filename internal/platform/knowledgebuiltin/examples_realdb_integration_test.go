//go:build integration

package knowledgebuiltin_test

// Every SQL example on a built-in page runs (#2448). A page's fenced blocks
// marked sql run in order in one fresh database per PostgreSQL major the
// application database kind supports, so a page's later examples read the
// tables its earlier ones made. The block's info string says how:
//
//	sql                   runs on every major
//	sql pg18              runs on 18 and later, the major that introduced it
//	sql refuses <state>   must fail with that SQLSTATE
//	sql no-run            a fragment shown, not run
//
// No example is carried over from a reference without being run here.

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgebuiltin"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// example is one fenced sql block.
type example struct {
	line    int
	sql     string
	minimum int
	refuses string
	skip    bool
}

// sqlExamples reads a page body's sql blocks.
func sqlExamples(t *testing.T, slug, body string) []example {
	t.Helper()
	var out []example
	var cur *example
	var b strings.Builder
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if cur == nil {
			if !strings.HasPrefix(trimmed, "```sql") {
				continue
			}
			cur = &example{line: i + 1}
			fields := strings.Fields(strings.TrimPrefix(trimmed, "```"))
			for j := 1; j < len(fields); j++ {
				switch {
				case fields[j] == "no-run":
					cur.skip = true
				case strings.HasPrefix(fields[j], "pg"):
					n, err := strconv.Atoi(strings.TrimPrefix(fields[j], "pg"))
					require.NoError(t, err, "%s line %d: %q is not a major", slug, i+1, fields[j])
					cur.minimum = n
				case fields[j] == "refuses" && j+1 < len(fields):
					cur.refuses = fields[j+1]
					j++
				default:
					t.Fatalf("%s line %d: unknown marker %q in %q", slug, i+1, fields[j], trimmed)
				}
			}
			b.Reset()
			continue
		}
		if trimmed == "```" {
			cur.sql = b.String()
			out = append(out, *cur)
			cur = nil
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	require.Nil(t, cur, "%s: an sql block is never closed", slug)
	return out
}

func TestBuiltinPages_RealDB_SQLExamplesRun(t *testing.T) {
	pages, err := knowledgebuiltin.Pages()
	require.NoError(t, err)
	total := 0
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		major, err := strconv.Atoi(strings.TrimPrefix(strings.SplitN(t.Name(), "/", 3)[1], "pg"))
		require.NoError(t, err)
		for _, p := range pages {
			examples := sqlExamples(t, p.Slug, p.Body)
			if len(examples) == 0 {
				continue
			}
			t.Run(p.Slug, func(t *testing.T) {
				db := testdb.AppDBTarget(t).TargetDB(t)
				db.SetMaxOpenConns(1)
				for _, ex := range examples {
					if ex.skip || major < ex.minimum {
						continue
					}
					total++
					runExample(t, db, p.Slug, ex)
				}
			})
		}
	})
	require.Positive(t, total, "no page holds an sql example, so this gate asserts nothing")
}

// runExample runs one block as one simple-protocol request.
func runExample(t *testing.T, db *sql.DB, slug string, ex example) {
	t.Helper()
	_, err := db.Exec(ex.sql)
	if ex.refuses == "" {
		require.NoError(t, err, "%s line %d:\n%s", slug, ex.line, ex.sql)
		return
	}
	var pe *pq.Error
	require.Truef(t, errors.As(err, &pe), "%s line %d must fail with %s, got %v:\n%s", slug, ex.line, ex.refuses, err, ex.sql)
	require.Equalf(t, ex.refuses, string(pe.Code), "%s line %d: %s:\n%s", slug, ex.line, pe.Message, ex.sql)
	// A refused block that opened a transaction stops at its error, so its
	// COMMIT never ran: end the transaction before the next block.
	_, _ = db.Exec("ROLLBACK")
}
