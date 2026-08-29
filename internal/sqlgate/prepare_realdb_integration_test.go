//go:build integration

package sqlgate_test

// The gate #1512 asks for: every SQL statement this repository holds is handed
// to a real PostgreSQL, which parses and plans it and executes nothing.
//
// It exists because no gate required a statement to be run by a database.
// sqlmock matches a query as a string and returns rows the test supplies, so
// #1506's SELECT -- whose unqualified column list was ambiguous across two
// joined tables -- passed its store test, counted as covered, and shipped.
//
// Two things are checked. Every statement whose text is known at compile time
// is prepared as written. A statement assembled at run time -- from a format
// string, through a builder library, or a clause at a time with += -- has no
// text in the source to prepare, so its package renders it through an
// SQLSamples the gate calls; a package that assembles SQL and renders nothing
// fails here rather than being skipped, so the gap is never silent.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptstore "github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	portalstore "github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/sqlgate"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	auditpg "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	promptpg "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// modulePattern loads every package in the module. Nothing is enumerated, so a
// new store package is covered the day it is written rather than the day
// somebody remembers to join it.
const modulePattern = "github.com/txn2/mcp-data-platform/..."

// pendingFile lists the packages that assemble SQL at run time and do not yet
// render it for this gate. It is a ratchet, not an exemption: a package that
// appears with dynamic SQL and is absent from the file fails, and a package
// that starts supplying samples and stays in the file fails too, so the list
// cannot quietly outlive what it describes.
const pendingFile = "templated_pending.txt"

// otherEngines are packages whose SQL is not PostgreSQL. pkg/query/trino and
// pkg/toolkits/trino build statements for Trino (the toolkit's existence lookup
// names a catalog's information_schema, which PostgreSQL reads as a database
// reference), whose dialect PostgreSQL does not parse; preparing them here
// would report a dialect difference as a defect.
var otherEngines = map[string]bool{
	"github.com/txn2/mcp-data-platform/pkg/query/trino":    true,
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino": true,
}

// source is a package that renders its run-time SQL for this gate.
type source struct {
	// samples renders one statement per builder, keyed by builder name.
	samples func() map[string]string
	// templated is how many statements the extractor finds assembled from a
	// format string in this package. It is asserted, so adding a builder to a
	// joined package fails until the builder is rendered here too -- without
	// it, "the package renders its SQL" would be satisfied forever by the
	// samples that were written the day it joined.
	templated int
}

// sampleSources are the packages that render their run-time SQL. Each is
// imported for its SQLSamples, so adding a package here is the whole of
// "joining" it.
var sampleSources = map[string]source{
	"github.com/txn2/mcp-data-platform/pkg/audit/postgres":            {auditpg.SQLSamples, 0},
	"github.com/txn2/mcp-data-platform/pkg/resource":                  {resource.SQLSamples, 6},
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore":   {portalstore.SQLSamples, 8},
	"github.com/txn2/mcp-data-platform/pkg/prompt/postgres":           {promptpg.SQLSamples, 5},
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore": {scriptstore.SQLSamples, 4},
}

func TestStoreStatementsPrepare_RealDB(t *testing.T) {
	stmts, facts, err := sqlgate.Extract(modulePattern)
	require.NoError(t, err, "extracting SQL from the module")
	require.NotEmpty(t, stmts, "the extractor found no SQL at all, which means it is broken rather than that the repo has none")

	db := testdb.New(t)
	ctx := context.Background()

	t.Run("every compile-time statement prepares", func(t *testing.T) {
		checked := 0
		for _, s := range stmts {
			if s.Kind != sqlgate.Constant || otherEngines[s.Package] {
				continue
			}
			checked++
			if err := prepare(ctx, db, s.SQL); err != nil {
				t.Errorf("PostgreSQL cannot prepare this statement:\n  %s\n  %v", s, err)
			}
		}
		t.Logf("prepared %d compile-time statements", checked)
		assert.Greater(t, checked, 100, "far fewer statements than this repository holds; the extractor is not seeing them")
	})

	t.Run("every rendered statement prepares", func(t *testing.T) {
		for _, pkg := range sortedKeys(sampleSources) {
			for name, stmt := range sampleSources[pkg].samples() {
				if err := prepare(ctx, db, stmt); err != nil {
					t.Errorf("PostgreSQL cannot prepare what %s.%s renders:\n  %s\n  %v",
						pkg, name, flatten(stmt), err)
				}
			}
		}
	})

	t.Run("a joined package renders every statement it assembles", func(t *testing.T) {
		for _, pkg := range sortedKeys(sampleSources) {
			assert.Equal(t, sampleSources[pkg].templated, facts[pkg].Templated,
				"%s assembles a different number of statements than when it joined: "+
					"render the new one in its SQLSamples and update the count in sampleSources",
				pkg)
		}
	})

	t.Run("a package that assembles SQL either renders it or is recorded as pending", func(t *testing.T) {
		pending, err := readPending(pendingFile)
		require.NoError(t, err)

		owes := map[string]bool{}
		for pkg, fact := range facts {
			if !otherEngines[pkg] && fact.AssembledAtRunTime() {
				owes[pkg] = true
			}
		}

		for pkg := range owes {
			_, renders := sampleSources[pkg]
			switch {
			case renders && pending[pkg]:
				t.Errorf("%s renders its SQL through SQLSamples and is still listed in %s; remove it from the file",
					pkg, pendingFile)
			case !renders && !pending[pkg]:
				t.Errorf("%s assembles SQL that no test can reach:%s\n"+
					"give the package an SQLSamples and add it to sampleSources, or record it in %s",
					pkg, sqlgate.Describe(sqlgate.TemplatedPackages(stmts)[pkg]), pendingFile)
			}
		}

		// The other direction: a line naming a package that no longer assembles
		// SQL is stale and is removed rather than left to rot.
		for pkg := range pending {
			if !owes[pkg] {
				t.Errorf("%s is listed in %s but assembles no SQL at run time; remove the line", pkg, pendingFile)
			}
		}
	})
}

// The gate is only as good as what the extractor calls a statement. These are
// the three judgments it has to get right, each anchored on a case in the tree:
// fold a concatenation of constants into one statement, do not mistake a
// fragment for one, and do not mistake prose for SQL.
func TestExtractorSeparatesStatementsFromFragments_RealDB(t *testing.T) {
	stmts, facts, err := sqlgate.Extract("github.com/txn2/mcp-data-platform/pkg/resource",
		"github.com/txn2/mcp-data-platform/pkg/prompt/postgres",
		"github.com/txn2/mcp-data-platform/pkg/audit/postgres",
		"github.com/txn2/mcp-data-platform/internal/platform/callrecord")
	require.NoError(t, err)

	find := func(substr string) []sqlgate.Statement {
		var out []sqlgate.Statement
		for _, s := range stmts {
			if strings.Contains(flatten(s.SQL), substr) {
				out = append(out, s)
			}
		}
		return out
	}

	// `"SELECT " + selectColumns + " FROM resources WHERE uri = $1"` is one
	// statement, carrying the columns the named constant holds -- the form
	// #1506's bug was written in.
	byURI := find("FROM resources WHERE uri = $1")
	require.Len(t, byURI, 1, "the concatenation is one statement, not one per operand")
	assert.Equal(t, sqlgate.Constant, byURI[0].Kind)
	assert.Contains(t, flatten(byURI[0].SQL), "display_name", "the named constant's columns are folded in")

	// A projection is part of a statement, not one of its own.
	assert.Empty(t, find("AS reuse_count"),
		"a parenthesized subquery aliased as a column is a fragment")

	// A SELECT that every caller completes with a GROUP BY is a fragment too,
	// and the completed form is what is reported.
	collection := find("FROM prompt_collections c LEFT JOIN prompts p")
	require.NotEmpty(t, collection)
	for _, s := range collection {
		assert.Contains(t, flatten(s.SQL), "GROUP BY",
			"only the rendering that carries its GROUP BY is a statement")
	}

	// A statement assembled around a run-time value is reported once, as
	// Templated, rather than as its constant half.
	count := find("SELECT COUNT(*) FROM resources WHERE")
	require.NotEmpty(t, count)
	for _, s := range count {
		assert.Equal(t, sqlgate.Templated, s.Kind)
	}

	// A package that composes every statement through a builder library writes
	// no SQL down at all, so the statements cannot speak for it: pkg/audit/postgres
	// builds its metrics SELECTs with squirrel, and only the Fact says so.
	audit := facts["github.com/txn2/mcp-data-platform/pkg/audit/postgres"]
	assert.True(t, audit.UsesBuilder, "the audit store composes SQL through squirrel")
	assert.True(t, audit.AssembledAtRunTime(),
		"a builder package owes a rendering even with no templated statement")

	// Prose that opens with a SQL keyword is not SQL.
	assert.False(t, sqlgate.IsSQL("delete prompt collection: %w"))
	assert.False(t, sqlgate.IsSQL("DELETE /api/v1/resources/{id}"))
	assert.True(t, sqlgate.IsSQL("DELETE FROM resources WHERE id = $1"))
}

// prepare asks PostgreSQL to parse and plan the statement. Nothing is executed,
// and the prepared statement is closed immediately.
func prepare(ctx context.Context, db *sql.DB, stmt string) error {
	ps, err := db.PrepareContext(ctx, stmt)
	if err != nil {
		return err //nolint:wrapcheck // the caller prints it beside the statement
	}
	return ps.Close() //nolint:wrapcheck // same
}

// readPending parses the ratchet file: one import path per line, blank lines and
// # comments ignored.
func readPending(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path in this package
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, nil
}

func sortedKeys(m map[string]source) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func flatten(sql string) string { return strings.Join(strings.Fields(sql), " ") }
