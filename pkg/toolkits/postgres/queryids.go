package postgres

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// platformStatements is every statement the toolkit runs on an application
// database for its own bookkeeping, each carrying sqltag.Platform, apart from
// the ones it builds per connection (platformStatementsOf).
var platformStatements = []string{
	typeQuery, attrQuery, ownQuery, lintTargetSQL, planChangesSQL, planLocksSQL, claimsSQL, roleProbeSQL,
	outboxPrivilegeSQL, notifyProbeSQL, probeSQL, cursorOpenSQL, ledgerExistsSQL,
	canCreateRoleSQL, roleExistsSQL, memberOfSQL,
	describeTablesSQL, describeColumnsSQL, describeConstraintsSQL, describeIndexesSQL, describeServerSQL,
	describeExtensionsSQL, describeAvailableSQL, describeFunctionsSQL, describeTriggersSQL, describeViewsSQL,
	describePoliciesSQL, describePartitionsSQL, describeEnumsSQL, describeDomainsSQL,
}

// platformStatementsOf is every statement the toolkit runs on c for its own
// bookkeeping: the fixed ones, the ledger read, and the reads and deletes of
// each outbox this process has drained.
func (c *conn) platformStatementsOf() []string {
	out := append([]string{c.ledgerReadSQL(), c.ledgerSchemasSQL()}, platformStatements...)
	var idents []string
	c.outboxes.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok {
			idents = append(idents, s)
		}
		return true
	})
	sort.Strings(idents)
	for _, ident := range idents {
		sel, del := outboxSQL(ident)
		out = append(out, sel, del)
	}
	return out
}

// queryIDBits is the width of a query id, a bigint.
const queryIDBits = 64

// queryIDPrefix opens the line EXPLAIN (VERBOSE) writes a statement's query
// identifier on.
const queryIDPrefix = "Query Identifier:"

// PlatformQueryIDs is the pg_stat_statements query id of every statement the
// toolkit runs on connection for its own bookkeeping, and of each of extra
// (#2536). pg_stat_statements keys an entry by the query id, which a comment
// does not change, and keeps the text of the first execution it saw: a
// statement first recorded before the platform tagged its statements keeps
// its untagged text, so the health report recognizes the platform's own
// statements by these ids as well as by the tag.
//
// Each id is read from EXPLAIN (GENERIC_PLAN, VERBOSE), which plans the
// statement with its placeholders unbound and runs nothing. A statement that
// does not plan here (an outbox table since dropped) is skipped. The ids are
// decimal text, as pg_stat_statements' queryid::text writes them. No id is
// read when the server does not compute them (compute_query_id off).
func (t *Toolkit) PlatformQueryIDs(ctx context.Context, connection string, extra []string) ([]string, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return nil, fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	run, release, err := c.explainer(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	seen := map[string]bool{}
	out := []string{}
	for _, stmt := range append(c.platformStatementsOf(), extra...) {
		id, ok := queryID(ctx, run, stmt)
		if ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// explainFunc runs one simple query and answers the first column of each row
// it returned.
type explainFunc func(ctx context.Context, sql string) ([]string, error)

// explainer is how c runs EXPLAIN: on one connection of its pool, which
// release returns, or a test's stand-in.
func (c *conn) explainer(ctx context.Context) (run explainFunc, release func(), err error) {
	if c.explain != nil {
		return c.explain, func() {}, nil
	}
	pc, err := c.acquire(ctx, c.pool)
	if err != nil {
		return nil, nil, err
	}
	// Sent as a simple query below pgx's query path, so a statement's own
	// placeholders reach the planner unbound rather than being taken for
	// arguments pgx must supply.
	run = func(ctx context.Context, sql string) ([]string, error) {
		results, err := pc.Conn().PgConn().Exec(ctx, sql).ReadAll()
		if err != nil {
			return nil, err //nolint:wrapcheck // the caller skips the statement
		}
		var lines []string
		for _, r := range results {
			for _, row := range r.Rows {
				if len(row) > 0 {
					lines = append(lines, string(row[0]))
				}
			}
		}
		return lines, nil
	}
	return run, pc.Release, nil
}

// queryID reads one statement's query id from its EXPLAIN.
func queryID(ctx context.Context, run explainFunc, stmt string) (string, bool) {
	lines, err := run(ctx, "/* mcp-data-platform */ EXPLAIN (GENERIC_PLAN, VERBOSE) "+stmt)
	if err != nil {
		return "", false
	}
	for _, line := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), queryIDPrefix)
		if !ok {
			continue
		}
		id := strings.TrimSpace(rest)
		if _, err := strconv.ParseInt(id, decimal, queryIDBits); err != nil {
			return "", false
		}
		return id, true
	}
	return "", false
}
