// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// ScratchTarget returns the catalog and schema table registrations write into
// on a connection, and whether that connection has one. An empty name resolves
// to the default connection, matching multiserver.Manager.Client("").
func (t *Toolkit) ScratchTarget(connection string) (ScratchConfig, bool) {
	t.catchUpUnbound(connection)
	t.connMu.RLock()
	defer t.connMu.RUnlock()

	if connection == "" {
		connection = t.name
	}
	target, ok := t.scratch[connection]
	return target, ok
}

// AcceptsWrites reports whether Exec would be allowed to run write SQL on a
// connection, without running any.
//
// It mirrors checkExecWritable exactly, including the two asymmetries that are
// easy to get backwards: a toolkit with no interceptor at all is a
// single-connection toolkit that was not configured read-only, so writes are
// ALLOWED; and an empty connection name resolves to the default, as it does
// everywhere else here.
//
// It exists so a surface can decline to offer a connection whose registration
// would be refused at DDL time. A form offering a connection the registration
// then refuses is the same defect as a registration refusing a connection the
// form offered.
func (t *Toolkit) AcceptsWrites(connection string) bool {
	t.catchUpUnbound(connection)
	t.connMu.RLock()
	defer t.connMu.RUnlock()

	if t.readOnly == nil {
		return true
	}
	if connection == "" {
		connection = t.name
	}
	return t.readOnly.AcceptsWrites(connection)
}

// Exec runs a statement against a named connection and discards its rows.
//
// It is the platform's one write path into Trino. The two direct callers of
// Manager().Client(name).Query -- the HTTP query func and trino_export -- run
// SELECT and reach the client without passing the read-only check, so neither
// is a model for a statement that writes. Exec runs the same
// ReadOnlyInterceptor the MCP tools run, against the same per-connection
// read_only settings that AddConnection and RemoveConnection maintain, so a
// read_only connection refuses DDL here exactly as trino_execute would.
//
// Authorization above this line is the caller's: Exec asks whether the
// connection accepts writes, never whether this person may use it.
func (t *Toolkit) Exec(ctx context.Context, connection, sql string) error {
	t.ServesConnection(ctx, connection)
	client, err := t.execClient(connection)
	if err != nil {
		return err
	}

	if err := t.checkExecWritable(ctx, connection, sql); err != nil {
		return err
	}

	// Limit is a row cap on the result set, which a DDL statement does not
	// have; it is left at the client's default rather than set to something
	// that would silently truncate a statement that does return rows. The
	// timeout is the scratch target's when it sets one: every statement Exec
	// runs creates, drops or fills a table in that target (#2131).
	opts := trinoclient.QueryOptions{Timeout: t.execTimeout(connection)}
	if _, err := t.telemetry.query(ctx, client, connection, sql, opts); err != nil {
		return fmt.Errorf("executing statement: %w", err)
	}
	return nil
}

// execTimeout is the statement timeout Exec runs with on a connection: its
// scratch target's statement_timeout, or zero for the connection's own.
func (t *Toolkit) execTimeout(connection string) time.Duration {
	target, _ := t.ScratchTarget(connection)
	return target.StatementTimeout
}

// TableLocation reports the external_location a table on a connection was
// created with, and whether the table exists at all. A table that exists with
// no external location reads as found with an empty location.
//
// It is how a registration learns whether a table it has no record of is one
// an earlier registration of the same file created (#2131): the location is
// the one fact the table carries about where it came from. It is a read, and
// runs past no write check.
func (t *Toolkit) TableLocation(ctx context.Context, connection, catalog, schema, table string) (location string, found bool, err error) {
	client, err := t.execClient(connection)
	if err != nil {
		return "", false, err
	}
	exists, err := t.lookupTable(ctx, client, connection, tableName{catalog, schema, table})
	if err != nil || !exists {
		return "", exists, err
	}
	stmt := "SHOW CREATE TABLE " + quoteIdentifier(catalog) + "." + quoteIdentifier(schema) + "." + quoteIdentifier(table)
	res, err := t.telemetry.query(ctx, client, connection, stmt, trinoclient.QueryOptions{Limit: 1})
	if err != nil {
		return "", true, fmt.Errorf("reading the definition of %s.%s.%s: %w", catalog, schema, table, err)
	}
	for _, row := range res.Rows {
		for _, v := range row {
			if text, ok := v.(string); ok {
				return externalLocationOf(text), true, nil
			}
		}
	}
	return "", true, nil
}

// SchemaTable is one table or view a schema holds, as information_schema
// names it.
type SchemaTable struct {
	Name string
	// View is true for a view, which has no location of its own.
	View bool
}

// maxSchemaTables bounds the tables ListTables returns.
const maxSchemaTables = 50000

// ListTables returns the tables and views a schema on a connection holds, by
// name, and whether the list was cut at maxSchemaTables (#2136). It is a read
// of information_schema, so it runs past no write check.
func (t *Toolkit) ListTables(ctx context.Context, connection, catalog, schema string) ([]SchemaTable, bool, error) {
	client, err := t.execClient(connection)
	if err != nil {
		return nil, false, err
	}
	stmt := "SELECT table_name, table_type FROM " + quoteIdentifier(catalog) +
		".information_schema.tables WHERE table_schema = " + quoteLiteral(schema) + " ORDER BY table_name"
	res, err := t.telemetry.query(ctx, client, connection, stmt, trinoclient.QueryOptions{Limit: maxSchemaTables + 1})
	if err != nil {
		return nil, false, fmt.Errorf("listing the tables of %s.%s: %w", catalog, schema, err)
	}
	out := make([]SchemaTable, 0, len(res.Rows))
	for _, row := range res.Rows {
		name, _ := row["table_name"].(string)
		kind, _ := row["table_type"].(string)
		if name != "" {
			out = append(out, SchemaTable{Name: name, View: kind == "VIEW"})
		}
	}
	if len(out) > maxSchemaTables {
		return out[:maxSchemaTables], true, nil
	}
	return out, false, nil
}

// externalLocationOf reads the external_location property out of the text
// SHOW CREATE TABLE returns, undoing the doubled quotes of its literal. It
// returns "" when the statement declares none.
func externalLocationOf(ddl string) string {
	const key = "external_location = '"
	_, rest, found := strings.Cut(ddl, key)
	if !found {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] != '\'' {
			_ = b.WriteByte(rest[i])
			continue
		}
		if i+1 < len(rest) && rest[i+1] == '\'' {
			_ = b.WriteByte('\'')
			i++
			continue
		}
		return b.String()
	}
	return ""
}

// TableExists reports whether a catalog on a connection holds a table, by
// asking its information_schema rather than by querying the table: a table
// that exists but cannot be read is still a table, and one whose metadata is
// gone is not, whatever its files say.
//
// It is a read, so it runs past no write check; the identifiers come from a
// registration row and are quoted here rather than trusted, because the
// connection's catalog names them and this is the one place they meet SQL
// text.
func (t *Toolkit) TableExists(ctx context.Context, connection, catalog, schema, table string) (bool, error) {
	client, err := t.execClient(connection)
	if err != nil {
		return false, err
	}
	return t.lookupTable(ctx, client, connection, tableName{catalog, schema, table})
}

// tableName is a table's catalog, schema and name.
type tableName struct{ catalog, schema, table string }

// lookupTable is TableExists on a client already resolved.
func (t *Toolkit) lookupTable(ctx context.Context, client *trinoclient.Client, connection string, n tableName) (bool, error) {
	stmt := "SELECT 1 AS present FROM " + quoteIdentifier(n.catalog) +
		".information_schema.tables WHERE table_schema = " + quoteLiteral(n.schema) +
		" AND table_name = " + quoteLiteral(n.table)
	res, err := t.telemetry.query(ctx, client, connection, stmt, trinoclient.QueryOptions{Limit: 1})
	if err != nil {
		return false, fmt.Errorf("looking up %s.%s.%s: %w", n.catalog, n.schema, n.table, err)
	}
	return len(res.Rows) > 0, nil
}

// quoteIdentifier renders a SQL identifier with its double quotes doubled.
func quoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral renders a SQL string literal with its single quotes doubled.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// execClient resolves the client a statement runs against.
func (t *Toolkit) execClient(connection string) (*trinoclient.Client, error) {
	if t.manager == nil {
		return nil, errors.New("no Trino client available")
	}
	var (
		client *trinoclient.Client
		err    error
	)
	if connection != "" {
		client, err = t.manager.Client(connection)
	} else {
		client, err = t.manager.DefaultClient()
	}
	if err != nil {
		return nil, fmt.Errorf("resolving trino connection: %w", err)
	}
	return client, nil
}

// checkExecWritable runs the statement past the read-only interceptor as the
// connection it is bound for.
//
// The interceptor reads the connection off the context, which on the MCP path
// its own middleware half puts there from the call's arguments. Exec has the
// name in hand, so it seeds the same value and calls the same Intercept: the
// decision, and the refusal wording a caller sees, come from one place.
func (t *Toolkit) checkExecWritable(ctx context.Context, connection, sql string) error {
	if t.readOnly == nil {
		return nil
	}
	if connection == "" {
		connection = t.name
	}
	ctx = context.WithValue(ctx, connectionContextKey{}, connection)
	if _, err := t.readOnly.Intercept(ctx, sql, trinotools.ToolExecute); err != nil {
		return err
	}
	return nil
}

// Query runs one read statement against a named connection (empty is the
// default) and returns its result, recorded like every other statement the
// toolkit sends (telemetry.go). It runs no read-only check: it is for a caller
// that issues a SELECT of its own; a statement that writes goes through Exec.
func (t *Toolkit) Query(ctx context.Context, connection, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	client, err := t.execClient(connection)
	if err != nil {
		return nil, err
	}
	res, err := t.telemetry.query(ctx, client, connection, sql, opts)
	if err != nil {
		return nil, fmt.Errorf("trino query: %w", err)
	}
	return res, nil
}
