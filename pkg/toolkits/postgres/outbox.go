package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// An application database as the input of a webhook source (#2408). The
// platform listens on a connection of its own, outside the pool, for
// notifications, and drains an outbox table: rows an application's trigger
// writes in the transaction that changed its data, each read, filed as an
// event, and deleted in one transaction, so a row is delivered once its event
// is stored and never lost to a notification no one heard.

// Outbox columns, as the outbox template creates them.
const (
	outboxColID      = "id"
	outboxColTable   = "source_table"
	outboxColOp      = "op"
	outboxColPayload = "payload"
	outboxColCreated = "created_at"
)

// maxChannelBytes is the longest channel name PostgreSQL takes: an
// identifier, NAMEDATALEN-1 bytes.
const maxChannelBytes = 63

// Notification is one notification a listener received.
type Notification struct {
	Channel string
	Payload string
	// PID is the server process that sent it.
	PID uint32
}

// Listener is a connection of its own listening on channels, outside the
// connection's pool and as the connection's role. It is not safe for
// concurrent use: one goroutine waits on it.
type Listener struct {
	conn *pgx.Conn
}

// Listen opens a connection to connection's database, passing the guard
// every connection passes, and listens on channels. A database reached
// through PgBouncer in transaction mode accepts the LISTEN and never
// delivers: the probe says so (ProbeConnection).
func (t *Toolkit) Listen(ctx context.Context, connection string, channels []string) (*Listener, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return nil, fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	conn, err := t.dedicated(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("appdb: listening on channel %q of connection %q: %s", ch, connection, scrubDriverError(err))
		}
	}
	return &Listener{conn: conn}, nil
}

// dedicated opens one connection with cfg's settings, outside every pool,
// and passes it through the guard in force.
func (t *Toolkit) dedicated(ctx context.Context, cfg Config) (*pgx.Conn, error) {
	pc, err := poolConfig(cfg, nil, nil)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, pc.ConnConfig)
	observe(cfg.ConnectionName, err)
	if err != nil {
		return nil, fmt.Errorf("appdb: connection %q could not reach its database: %s", cfg.ConnectionName, scrubDriverError(err))
	}
	if err := t.afterConnect(ctx, conn); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return conn, nil
}

// Wait blocks until a notification arrives or ctx ends.
func (l *Listener) Wait(ctx context.Context) (Notification, error) {
	n, err := l.conn.WaitForNotification(ctx)
	if err != nil {
		return Notification{}, err //nolint:wrapcheck // the caller tells a deadline from a lost connection by its type
	}
	return Notification{Channel: n.Channel, Payload: n.Payload, PID: n.PID}, nil
}

// Close ends the listener's connection.
func (l *Listener) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	_ = l.conn.Close(ctx)
}

// OutboxRow is one row of an outbox table.
type OutboxRow struct {
	ID int64
	// Table is the schema-qualified table whose change wrote the row.
	Table string
	// Op is the change: INSERT, UPDATE or DELETE.
	Op string
	// Payload is the row as JSON: the new row, or the old one for a DELETE.
	Payload   string
	CreatedAt time.Time
}

// DrainOutbox reads up to limit of table's oldest rows, hands them to write,
// and deletes them in the same transaction once write returned nil. A row
// another session holds is skipped (FOR UPDATE SKIP LOCKED). It returns how
// many rows it read; write is not called when there were none. A write that
// fails leaves every row where it was, for the next drain.
func (t *Toolkit) DrainOutbox(ctx context.Context, connection, table string, limit int, write func([]OutboxRow) error) (int, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return 0, fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	ident, err := OutboxIdentifier(table)
	if err != nil {
		return 0, err
	}
	sel, del := outboxSQL(ident)
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite}, c.cfg.StatementTimeout, true, func(tx pgx.Tx) (any, error) {
		rows, err := readOutbox(ctx, tx, sel, limit)
		if err != nil || len(rows) == 0 {
			return rows, err
		}
		if err := write(rows); err != nil {
			return nil, &writeError{err: err}
		}
		ids := make([]int64, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		if _, err := tx.Exec(ctx, del, ids); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
		return rows, nil
	})
	observe(connection, err)
	if err != nil {
		if w, ok := errors.AsType[*writeError](err); ok {
			return 0, w.err
		}
		return 0, c.failure(ctx, err)
	}
	rows, _ := out.([]OutboxRow)
	return len(rows), nil
}

// outboxSQL is what a drain of the outbox ident runs: the read of its oldest
// rows, locking them and skipping the ones another session holds, and the
// delete of the rows it filed.
func outboxSQL(ident string) (sel, del string) {
	sel = fmt.Sprintf("SELECT %s, %s, %s, %s::text, %s FROM %s ORDER BY %s LIMIT $1::int FOR UPDATE SKIP LOCKED",
		outboxColID, outboxColTable, outboxColOp, outboxColPayload, outboxColCreated, ident, outboxColID)
	del = fmt.Sprintf("DELETE FROM %s WHERE %s = ANY($1::bigint[])", ident, outboxColID)
	return sel, del
}

// writeError is a drain's write that failed, told apart from the database's
// failures so it reaches the caller as written.
type writeError struct{ err error }

func (w *writeError) Error() string { return w.err.Error() }
func (w *writeError) Unwrap() error { return w.err }

// readOutbox reads the rows a drain takes.
func readOutbox(ctx context.Context, tx pgx.Tx, sel string, limit int) ([]OutboxRow, error) {
	rs, err := tx.Query(ctx, sel, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by its type
	}
	defer rs.Close()
	out := []OutboxRow{}
	for rs.Next() {
		var r OutboxRow
		if err := rs.Scan(&r.ID, &r.Table, &r.Op, &r.Payload, &r.CreatedAt); err != nil {
			return nil, err //nolint:wrapcheck // read by its type
		}
		out = append(out, r)
	}
	return out, rs.Err() //nolint:wrapcheck // read by its type
}

// OutboxTemplateName is the manage_schema add template that writes an outbox.
const OutboxTemplateName = "outbox"

// splitTable reads "schema.table", or a bare table in defaultSchema, as two
// lowercase identifiers.
func splitTable(name, defaultSchema string) (schema, table string, err error) {
	schema, table, qualified := strings.Cut(strings.TrimSpace(name), ".")
	if !qualified {
		schema, table = defaultSchema, schema
	}
	if !ledgerSchemaName.MatchString(schema) || !ledgerSchemaName.MatchString(table) {
		return "", "", fmt.Errorf("appdb: %q is not a table named by lowercase identifiers, as schema.table", name)
	}
	return schema, table, nil
}

// OutboxChannel is the channel an outbox's trigger notifies and its webhook
// source listens on: the table's qualified name.
func OutboxChannel(schema, table string) string { return schema + "." + table }

// OutboxIdentifier is an outbox table, named "schema.table", as SQL: its two
// identifiers checked and quoted. It refuses a name whose channel PostgreSQL
// would not take.
func OutboxIdentifier(name string) (string, error) {
	schema, table, err := splitTable(name, "")
	if err != nil {
		return "", err
	}
	if len(OutboxChannel(schema, table)) > maxChannelBytes {
		return "", fmt.Errorf("appdb: outbox %s.%s is longer than the %d bytes a notification channel takes", schema, table, maxChannelBytes)
	}
	return pgx.Identifier{schema, table}.Sanitize(), nil
}

// OutboxTemplate is the migration that creates an outbox table in schema,
// the trigger function that writes a changed row to it and, when notify is
// set, notifies its channel, and a trigger on each table in capture (a bare
// name is in schema).
// down removes the triggers, the function and the table.
func OutboxTemplate(schema, table string, capture []string, notify bool) (up, down string, err error) {
	ident, err := OutboxIdentifier(schema + "." + table)
	if err != nil {
		return "", "", err
	}
	fn := pgx.Identifier{schema, table + "_capture"}.Sanitize()
	trigger := pgx.Identifier{table + "_capture"}.Sanitize()
	var u, d strings.Builder
	fmt.Fprintf(&u, `CREATE TABLE %s (
    %s bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    %s text NOT NULL,
    %s text NOT NULL,
    %s jsonb NOT NULL,
    %s timestamptz NOT NULL DEFAULT now()
);
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $outbox$
BEGIN
    INSERT INTO %s (%s, %s, %s)
    VALUES (TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME, TG_OP, to_jsonb(CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END));
%s    RETURN NULL;
END
$outbox$;
`, ident, outboxColID, outboxColTable, outboxColOp, outboxColPayload, outboxColCreated,
		fn, ident, outboxColTable, outboxColOp, outboxColPayload, notifyLine(schema, table, notify))
	for _, name := range capture {
		cs, ct, err := splitTable(name, schema)
		if err != nil {
			return "", "", err
		}
		target := pgx.Identifier{cs, ct}.Sanitize()
		fmt.Fprintf(&u, "CREATE TRIGGER %s AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s();\n", trigger, target, fn)
		fmt.Fprintf(&d, "DROP TRIGGER IF EXISTS %s ON %s;\n", trigger, target)
	}
	fmt.Fprintf(&d, "DROP FUNCTION IF EXISTS %s();\nDROP TABLE IF EXISTS %s;\n", fn, ident)
	return u.String(), d.String(), nil
}

// notifyLine is the capture function's notification of the outbox channel,
// or nothing when notify is off. A transaction that notifies takes a lock
// held across every notifying commit, which serializes a table written by
// many sessions at once; without it the source's poll alone finds the rows.
func notifyLine(schema, table string, notify bool) string {
	if !notify {
		return ""
	}
	return "    PERFORM pg_notify('" + OutboxChannel(schema, table) + "', '');\n"
}

// listenProbeWait is how long the probe waits for its own notification.
const listenProbeWait = 2 * time.Second

// probeListen reports whether a notification sent through the pool reaches a
// listening connection: through PgBouncer in transaction mode it never does,
// and a webhook source with input postgres hears nothing on the connection.
func (t *Toolkit) probeListen(ctx context.Context, c *conn) string {
	channel := "mcp_probe_" + strings.ReplaceAll(c.cfg.ConnectionName, "-", "_")
	if len(channel) > maxChannelBytes {
		channel = channel[:maxChannelBytes]
	}
	l, err := t.Listen(ctx, c.cfg.ConnectionName, []string{channel})
	if err != nil {
		return "; LISTEN failed, so a webhook source with input postgres cannot use this connection"
	}
	defer l.Close()
	if _, err := c.pool.Exec(ctx, "SELECT pg_notify($1, 'probe')", channel); err != nil {
		return "; NOTIFY failed, so a webhook source with input postgres cannot use this connection"
	}
	wctx, cancel := context.WithTimeout(ctx, listenProbeWait)
	defer cancel()
	if _, err := l.Wait(wctx); err != nil {
		return "; a notification sent through the pool did not arrive within " + listenProbeWait.String() +
			" (PgBouncer in transaction mode delivers none), so a webhook source with input postgres would hear nothing"
	}
	return "; LISTEN/NOTIFY delivers"
}

// errOutboxPrivileges refuses an outbox the connection's role cannot both
// read and delete from.
var errOutboxPrivileges = errors.New("the connection's role needs SELECT and DELETE on the outbox")

// outboxPrivilegeSQL asks whether the connection's role may read and delete
// from an outbox; it fails (42P01) when the table does not exist.
const outboxPrivilegeSQL = "SELECT has_table_privilege($1::text, 'SELECT') AND has_table_privilege($1::text, 'DELETE')"

// CheckOutbox refuses an outbox table that does not exist on connection, or
// that the connection's role cannot read and delete from, which a drain does.
func (t *Toolkit) CheckOutbox(ctx context.Context, connection, table string) error {
	c, ok := t.lookup(connection)
	if !ok {
		return fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	if _, err := OutboxIdentifier(table); err != nil {
		return err
	}
	out, err := c.inTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, c.cfg.StatementTimeout, false, func(tx pgx.Tx) (any, error) {
		var allowed bool
		err := tx.QueryRow(ctx, outboxPrivilegeSQL, table).Scan(&allowed)
		return allowed, err //nolint:wrapcheck // read by its type
	})
	observe(connection, err)
	if err != nil {
		return c.failure(ctx, err)
	}
	if allowed, _ := out.(bool); !allowed {
		return fmt.Errorf("appdb: outbox %s on connection %q: %w", table, connection, errOutboxPrivileges)
	}
	return nil
}
