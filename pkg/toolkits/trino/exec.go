// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"context"
	"errors"
	"fmt"
	"strings"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// ScratchTarget returns the catalog and schema table registrations write into
// on a connection, and whether that connection has one. An empty name resolves
// to the default connection, matching multiserver.Manager.Client("").
func (t *Toolkit) ScratchTarget(connection string) (ScratchConfig, bool) {
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
	client, err := t.execClient(connection)
	if err != nil {
		return err
	}

	if err := t.checkExecWritable(ctx, connection, sql); err != nil {
		return err
	}

	// Limit is a row cap on the result set, which a DDL statement does not
	// have; it is left at the client's default rather than set to something
	// that would silently truncate a statement that does return rows.
	if _, err := client.Query(ctx, sql, trinoclient.QueryOptions{}); err != nil {
		return fmt.Errorf("executing statement: %w", err)
	}
	return nil
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
	stmt := "SELECT 1 AS present FROM " + quoteIdentifier(catalog) +
		".information_schema.tables WHERE table_schema = " + quoteLiteral(schema) +
		" AND table_name = " + quoteLiteral(table)
	res, err := client.Query(ctx, stmt, trinoclient.QueryOptions{Limit: 1})
	if err != nil {
		return false, fmt.Errorf("looking up %s.%s.%s: %w", catalog, schema, table, err)
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
	if t.manager != nil {
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
	if t.client == nil {
		return nil, errors.New("no Trino client available")
	}
	return t.client, nil
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
