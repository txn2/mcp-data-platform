package scripthandler

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migrationsDir is the embedded migration set, read from the tree.
var migrationsDir = filepath.Join("..", "..", "..", "pkg", "database", "migrate", "migrations")

// viewStatement matches the statement that defines the script_handlers view,
// up to the statement's end.
var viewStatement = regexp.MustCompile(`(?is)CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+script_handlers\s+AS\s+(.*?);`)

// branchKind reads the kind column literal each UNION ALL branch selects
// second, after the handler's id: SELECT e.id, 'endpoint'::text AS kind.
var branchKind = regexp.MustCompile(`(?i)SELECT\s+\w+\.id\s*,\s*'([a-z_]+)'::text`)

// literal is every quoted literal in the statement, which is every kind and
// type the view can report.
var literal = regexp.MustCompile(`'([a-z_]+)'`)

// newestViewDefinition is the body of the script_handlers view as the newest
// up migration defining it writes it, and that migration's name.
func newestViewDefinition(t *testing.T) (body, file string) {
	t.Helper()
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range slices.Backward(names) {
		data, err := os.ReadFile(filepath.Join(migrationsDir, name)) //nolint:gosec // a migration file of this repository, named by its own listing
		require.NoError(t, err)
		if m := viewStatement.FindSubmatch(data); m != nil {
			return string(m[1]), name
		}
	}
	t.Fatal("no up migration defines VIEW script_handlers")
	return "", ""
}

// TestTheViewNamesEveryRegisteredKind is #2441's structure test: the
// script_handlers view that every handler list and the type filter read
// must hold a branch for each registered kind and name no kind the platform
// does not register, and every type it reports must be one a list filters
// on. A kind added in Go without its table in the view would be a handler
// no list shows; a kind left in the view after it was retired would be one
// no command can act on.
func TestTheViewNamesEveryRegisteredKind(t *testing.T) {
	body, file := newestViewDefinition(t)

	branches := branchKind.FindAllStringSubmatch(body, -1)
	inView := make([]string, 0, len(branches))
	for _, m := range branches {
		inView = append(inView, m[1])
	}
	require.NotEmpty(t, inView, "%s: no UNION ALL branch selects a kind literal after the id", file)

	registered := Kinds()
	for _, k := range registered {
		assert.Contains(t, inView, k, "%s: the view has no branch for the registered kind %q", file, k)
	}
	for _, k := range inView {
		assert.Contains(t, registered, k, "%s: the view names the kind %q, which the platform does not register", file, k)
	}
	assert.Len(t, inView, len(registered), "%s: one branch per kind", file)

	for _, m := range literal.FindAllStringSubmatch(body, -1) {
		assert.True(t, slices.Contains(Types(), m[1]),
			"%s: the view reports %q, which is no type a handler list filters on (%v)", file, m[1], Types())
	}
}

// TestTheViewReaderMatchesItsDefinition pins the parse the structure test
// relies on against a definition of known shape, so a pattern that matched
// nothing could not pass it vacuously.
func TestTheViewReaderMatchesItsDefinition(t *testing.T) {
	sql := `CREATE OR REPLACE VIEW script_handlers AS
SELECT e.id, 'endpoint'::text AS kind, CASE WHEN s.is_form THEN 'form' ELSE 'endpoint' END AS type
  FROM script_endpoints e JOIN scripts s ON s.id = e.script_id
UNION ALL
SELECT c.id, 'schedule'::text, 'schedule'::text FROM script_schedules c;
SELECT 'ignored'::text;`
	m := viewStatement.FindStringSubmatch(sql)
	require.NotNil(t, m)
	branches := branchKind.FindAllStringSubmatch(m[1], -1)
	kinds := make([]string, 0, len(branches))
	for _, k := range branches {
		kinds = append(kinds, k[1])
	}
	assert.Equal(t, []string{"endpoint", "schedule"}, kinds)
	assert.NotContains(t, m[1], "ignored", "the statement ends at its semicolon")
}
