package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// SchemaToolName is the one agent tool this kind registers (#2404): the
// versioned migrations an application database's schema is built from. It
// is part of building an application, not a data tool, and acts only on
// connections of this kind.
const SchemaToolName = "manage_schema"

// The manage_schema commands.
const (
	schemaStatus   = "status"
	schemaAdd      = "add"
	schemaPlan     = "plan"
	schemaApply    = "apply"
	schemaDown     = "down"
	schemaHistory  = "history"
	schemaDescribe = "describe"
)

// The events a migration's history records.
const (
	EventAdded    = "added"
	EventEdited   = "edited"
	EventApplied  = "applied"
	EventReverted = "reverted"
	EventFailed   = "failed"
)

// MigrationEvent is one entry of a migration's history.
type MigrationEvent struct {
	Connection string    `json:"connection"`
	Schema     string    `json:"schema"`
	Version    int       `json:"version"`
	Event      string    `json:"event"`
	Actor      string    `json:"actor,omitempty"`
	Checksum   string    `json:"checksum,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Detail     string    `json:"detail,omitempty"`
	At         time.Time `json:"at"`
}

// MigrationStore keeps the migrations and their history in the platform's
// database.
type MigrationStore interface {
	// List is every migration of a connection's schema, oldest version first.
	// An empty schema is every schema of the connection, in schema order.
	List(ctx context.Context, connection, schema string) ([]Migration, error)
	// Put adds a migration or replaces a pending one, reporting whether it
	// replaced one.
	Put(ctx context.Context, m Migration) (replaced bool, err error)
	// Record appends to a migration's history.
	Record(ctx context.Context, e MigrationEvent) error
	// Events is a schema's history, newest first.
	Events(ctx context.Context, connection, schema string, limit int) ([]MigrationEvent, error)
}

// SetMigrationStore installs the store manage_schema keeps migrations in.
// Without one the kind registers no tool. It is set before tools register.
func (t *Toolkit) SetMigrationStore(s MigrationStore) { t.migrations = s }

// SetSchemaChecks installs what status and describe add to their answers
// (#2406, #2409): drift, the live schema against the one its applied
// migrations build, and findings over a description (an unused index, a dead
// tuple ratio). Each answers a JSON value; nil adds nothing.
func (t *Toolkit) SetSchemaChecks(drift func(ctx context.Context, connection, schema string) any,
	findings func(ctx context.Context, connection, schema string, d *Description) any,
) {
	t.driftCheck, t.findings = drift, findings
}

// schemaInput is manage_schema's arguments.
type schemaInput struct {
	Command        string `json:"command"`
	Connection     string `json:"connection"`
	Schema         string `json:"schema,omitempty"`
	Version        int    `json:"version,omitempty"`
	Name           string `json:"name,omitempty"`
	Up             string `json:"up,omitempty"`
	Down           string `json:"down,omitempty"`
	Transactional  *bool  `json:"transactional,omitempty"`
	ToVersion      *int   `json:"to_version,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	ChangeSummary  string `json:"change_summary,omitempty"`
	UserAgreed     bool   `json:"user_agreed,omitempty"`
	// Template, OutboxTable, Capture and Notify write a migration the
	// platform provides in place of up and down (#2408).
	Template    string   `json:"template,omitempty"`
	OutboxTable string   `json:"outbox_table,omitempty"`
	Capture     []string `json:"capture,omitempty"`
	Notify      *bool    `json:"notify,omitempty"`
}

// schemaToolSchema is manage_schema's input schema.
var schemaToolSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["command", "connection"],
  "properties": {
    "command": {"type": "string", "enum": ["status", "add", "plan", "apply", "down", "history", "describe"],
      "description": "status: what the ledger says is applied and what is pending. add: add a migration, or replace one still pending; it is linted for statements that lock, rewrite or remove data, and applied from zero on a throwaway database before it saves. plan: run the pending migrations in a transaction, report the objects they change and the locks they take, and roll back. apply: apply the pending migrations. down: revert the latest applied migration, or every one above to_version. history: what happened to each migration. describe: the server's major and version, the extensions the database holds and the ones it offers, and every object the applied migrations produced in schema: tables with their columns, constraints, indexes, row-level security, policies, triggers and partitions, views with their definitions, functions and procedures, enums and domains."},
    "connection": {"type": "string", "description": "The application database (a connection of kind appdb)."},
    "schema": {"type": "string", "description": "The schema (namespace) the migrations belong to and run in. Defaults to public."},
    "version": {"type": "integer", "minimum": 1, "description": "add: the migration's version, above every applied version."},
    "name": {"type": "string", "description": "add: what the migration does, in a few words."},
    "up": {"type": "string", "description": "add: the SQL that applies the migration. It may hold several statements."},
    "down": {"type": "string", "description": "add: the SQL that reverts it. Without it the migration cannot be reverted."},
    "transactional": {"type": "boolean", "description": "add: false for a migration holding a statement that cannot run in a transaction (CREATE INDEX CONCURRENTLY), which then runs alone and is not planned. Defaults to true."},
    "to_version": {"type": "integer", "minimum": 0, "description": "plan and apply: stop at this version. down: revert every applied migration above it."},
    "timeout_seconds": {"type": "integer", "minimum": 1, "description": "apply and down: how long one statement of a migration may run. Defaults to the connection's statement_timeout, at most 600."},
    "limit": {"type": "integer", "minimum": 1, "description": "history: the most entries to return. Defaults to 100."},
    "change_summary": {"type": "string", "description": "add: what a migration that removes data (a dropped table or column, TRUNCATE, a DELETE or UPDATE with no WHERE) removes, in plain words, as the person the application is built for agreed to it. Recorded in the migration's history."},
    "user_agreed": {"type": "boolean", "description": "add: true once the person the application is built for agreed to change_summary."},
    "template": {"type": "string", "enum": ["outbox"], "description": "add: write a migration the platform provides in place of up and down. outbox: an outbox table, the trigger function that writes each changed row of a captured table to it and notifies its channel, and a trigger on each table in capture; a webhook source with input postgres and mode outbox files each row as an event."},
    "outbox_table": {"type": "string", "description": "add with template outbox: the outbox table's name in schema. Defaults to outbox."},
    "capture": {"type": "array", "items": {"type": "string"}, "description": "add with template outbox: the tables whose inserted, updated and deleted rows are written to the outbox, as table (in schema) or schema.table."},
    "notify": {"type": "boolean", "description": "add with template outbox: whether each captured write notifies the outbox channel (default true). Set false for a table many sessions write at once: a notifying commit takes a lock every other notifying commit waits on, and the webhook source's poll finds the rows without it."}
  }
}`)

// registerSchemaTool registers manage_schema.
func (t *Toolkit) registerSchemaTool(s *mcp.Server) {
	toolkit.AddTool(s, &mcp.Tool{
		Name:  SchemaToolName,
		Title: "Manage Schema",
		Description: "Build an application database's schema from versioned migrations the platform keeps, applies " +
			"and reports, never ad hoc DDL. Add each change as a migration (version, name, up SQL, optional down " +
			"SQL), plan it to see the objects it changes and the locks it takes, then apply it. An applied migration " +
			"is immutable: a fix is a new migration. describe returns the schema the applied migrations produced, " +
			"which is how to read an application's tables while writing its scripts. Acts only on connections of " +
			"kind appdb.",
		InputSchema: schemaToolSchema,
		Annotations: toolkit.WriteAnnotations(true),
	}, t.handleSchema)
}

// handleSchema runs one manage_schema command.
func (t *Toolkit) handleSchema(ctx context.Context, _ *mcp.CallToolRequest, in schemaInput) (*mcp.CallToolResult, any, error) {
	if in.Schema == "" {
		in.Schema = DefaultSchema
	}
	if in.Command == schemaAdd {
		templated, err := fromTemplate(in)
		if err != nil {
			return toolkit.ErrorResult(err.Error()), nil, nil
		}
		in = templated
	}
	out, err := t.runSchema(ctx, in)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return toolkit.JSONResult(out), nil, nil
}

// runSchema dispatches a command.
func (t *Toolkit) runSchema(ctx context.Context, in schemaInput) (any, error) {
	c, err := t.schemaConn(in.Connection, in.Schema)
	if err != nil {
		return nil, err
	}
	switch in.Command {
	case schemaStatus:
		return t.schemaStatus(ctx, in)
	case schemaAdd:
		return t.schemaAdd(ctx, c, in)
	case schemaPlan:
		return t.schemaPlan(ctx, c, in)
	case schemaApply, schemaDown:
		return t.schemaChange(ctx, c, in)
	case schemaHistory:
		return t.schemaHistory(ctx, in)
	case schemaDescribe:
		return t.schemaDescribe(ctx, in)
	default:
		return nil, fmt.Errorf("appdb: unknown command %q; the commands are status, add, plan, apply, down, history and describe", in.Command)
	}
}

// schemaDescribe is describe: the schema read from the catalog, with the
// findings over it when they are installed.
func (t *Toolkit) schemaDescribe(ctx context.Context, in schemaInput) (any, error) {
	d, err := t.Describe(ctx, in.Connection, in.Schema)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		keyConnection: in.Connection, keySchema: in.Schema, "server": d.Server,
		"extensions": d.Extensions, "available_extensions": d.AvailableExtensions, "tables": d.Tables,
		"functions": d.Functions, "enums": d.Enums, "domains": d.Domains,
	}
	if t.findings != nil {
		out["findings"] = t.findings(ctx, in.Connection, in.Schema, d)
	}
	return out, nil
}

// schemaHistory is history: the newest events first, at most maxHistory.
func (t *Toolkit) schemaHistory(ctx context.Context, in schemaInput) (any, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultHistory
	}
	events, err := t.migrations.Events(ctx, in.Connection, in.Schema, min(limit, maxHistory))
	if err != nil {
		return nil, fmt.Errorf("appdb: reading the history: %w", err)
	}
	return map[string]any{keyConnection: in.Connection, keySchema: in.Schema, "events": events}, nil
}

// errReadMigrations is the failure to read the platform's migrations.
const errReadMigrations = "appdb: reading the migrations: %w"

// doneBeforeError is an apply or revert that failed after others it made
// succeeded, naming them.
type doneBeforeError struct {
	err  error
	done []int
}

func (e *doneBeforeError) Error() string {
	return fmt.Sprintf("%v (done before it: %v)", e.err, e.done)
}
func (e *doneBeforeError) Unwrap() error { return e.err }

// The keys every manage_schema answer names its target under.
const (
	keyConnection = "connection"
	keySchema     = "schema"
)

// History bounds.
const (
	defaultHistory = 100
	maxHistory     = 1000
)

// pendingRef is a pending migration as status and plan name it.
type pendingRef struct {
	Version       int    `json:"version"`
	Name          string `json:"name"`
	Transactional bool   `json:"transactional"`
	Checksum      string `json:"checksum"`
}

func refsOf(ms []Migration) []pendingRef {
	out := make([]pendingRef, len(ms))
	for i, m := range ms {
		out[i] = pendingRef{Version: m.Version, Name: m.Name, Transactional: m.Transactional, Checksum: m.Checksum}
	}
	return out
}

// schemaStatus is status: the ledger, the pending migrations and any drift.
func (t *Toolkit) schemaStatus(ctx context.Context, in schemaInput) (any, error) {
	migrations, err := t.migrations.List(ctx, in.Connection, in.Schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	st, err := t.Status(ctx, in.Connection, in.Schema, migrations)
	if err != nil {
		return nil, err
	}
	applied, pending, drift := st.Applied, st.Pending, st.Drift
	c, _ := t.lookup(in.Connection)
	out := map[string]any{
		keyConnection: in.Connection, keySchema: in.Schema, "ledger": c.ledger(),
		"current_version": currentOf(applied), "applied": applied, "pending": refsOf(pending), "drift": nonNilDrift(drift),
	}
	if t.driftCheck != nil {
		out["schema_drift"] = t.driftCheck(ctx, in.Connection, in.Schema)
	}
	return out, nil
}

func nonNilDrift(d []Drift) []Drift {
	if d == nil {
		return []Drift{}
	}
	return d
}

// schemaAdd is add: a new migration, or a replacement of one still pending.
func (t *Toolkit) schemaAdd(ctx context.Context, c *conn, in schemaInput) (any, error) {
	if err := writable(c); err != nil {
		return nil, err
	}
	transactional := in.Transactional == nil || *in.Transactional
	if err := checkAdd(in, transactional); err != nil {
		return nil, err
	}
	migrations, err := t.migrations.List(ctx, in.Connection, in.Schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	applied, err := c.readLedger(ctx, c.reader(), in.Schema)
	if err != nil {
		return nil, c.failure(ctx, err)
	}
	if err := addable(in.Version, applied); err != nil {
		return nil, err
	}
	m := Migration{
		Connection: in.Connection, Schema: in.Schema, Version: in.Version, Name: strings.TrimSpace(in.Name),
		Up: in.Up, Down: in.Down, Transactional: transactional,
		Checksum: ChecksumOf(in.Up, in.Down, transactional), Author: actorOf(ctx),
	}
	if holds(migrations, m) {
		return map[string]any{"migration": refsOf([]Migration{m})[0], "status": StatePending, "unchanged": true}, nil
	}
	findings, err := t.review(ctx, in, m)
	if err != nil {
		return nil, err
	}
	replaced, err := t.migrations.Put(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("appdb: saving the migration: %w", err)
	}
	event := EventAdded
	if replaced {
		event = EventEdited
	}
	t.recordAdded(ctx, m, event, reviewDetail(in, m.Author, findings))
	return map[string]any{"migration": refsOf([]Migration{m})[0], "status": StatePending, "replaced": replaced, "findings": findings}, nil
}

// checkAdd refuses an add missing what a migration needs, or whose SQL would
// end its own transaction.
func checkAdd(in schemaInput, transactional bool) error {
	switch {
	case in.Version < 1:
		return errors.New("appdb: add needs a version of 1 or more")
	case strings.TrimSpace(in.Name) == "":
		return errors.New("appdb: add needs a name saying what the migration does")
	case strings.TrimSpace(in.Up) == "":
		return errors.New("appdb: add needs the up SQL")
	}
	if err := checkMigrationSQL(in.Up, transactional); err != nil {
		return &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: up SQL: %v", errPrefix, err)}
	}
	if in.Down == "" {
		return nil
	}
	if err := checkMigrationSQL(in.Down, true); err != nil {
		return &RefusedError{Code: CodeInvalidStatement, msg: fmt.Sprintf("%s: down SQL: %v", errPrefix, err)}
	}
	return nil
}

// defaultOutboxTable is the outbox template's table when it names none.
const defaultOutboxTable = "outbox"

// untemplated refuses the template's arguments on a migration naming none.
func untemplated(in schemaInput) error {
	if in.OutboxTable != "" || len(in.Capture) > 0 || in.Notify != nil {
		return errors.New("appdb: outbox_table, capture and notify are for template outbox")
	}
	return nil
}

// notifies is the outbox template's notify argument, on unless set false.
func notifies(in schemaInput) bool { return in.Notify == nil || *in.Notify }

// fromTemplate fills in's up, down and name from the template it names, and
// refuses a template beside SQL of the caller's own.
func fromTemplate(in schemaInput) (schemaInput, error) {
	if in.Template == "" {
		return in, untemplated(in)
	}
	if in.Template != OutboxTemplateName {
		return in, fmt.Errorf("appdb: unknown template %q; the one template is %s", in.Template, OutboxTemplateName)
	}
	if in.Up != "" || in.Down != "" {
		return in, errors.New("appdb: a template writes up and down itself; send one or the other")
	}
	table := in.OutboxTable
	if table == "" {
		table = defaultOutboxTable
	}
	up, down, err := OutboxTemplate(in.Schema, table, in.Capture, notifies(in))
	if err != nil {
		return in, err
	}
	in.Up, in.Down = up, down
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "outbox " + OutboxChannel(in.Schema, table)
	}
	return in, nil
}

// holds reports whether the platform already keeps m exactly.
func holds(migrations []Migration, m Migration) bool {
	for _, have := range migrations {
		if have.Version == m.Version && have.Checksum == m.Checksum && have.Name == m.Name {
			return true
		}
	}
	return false
}

// addable refuses a version the ledger holds (an applied migration is
// immutable) and one below the latest applied.
func addable(version int, applied []Applied) error {
	latest := 0
	for _, a := range applied {
		if a.Version == version {
			return &RefusedError{Code: CodeMigrationApplied, msg: fmt.Sprintf(
				"%s: migration %d is applied, and an applied migration is immutable; add the fix as a new migration above version %d",
				errPrefix, version, applied[len(applied)-1].Version)}
		}
		latest = max(latest, a.Version)
	}
	if version < latest {
		return &RefusedError{Code: CodeMigrationApplied, msg: fmt.Sprintf(
			"%s: version %d is below version %d, which is applied; give the migration a version above %d",
			errPrefix, version, latest, latest)}
	}
	return nil
}

// CodeMigrationApplied refuses a change to an applied migration.
const CodeMigrationApplied = "migration_applied"

// writable refuses add and plan on a read_only connection: both need to write.
func writable(c *conn) error {
	if c.cfg.ReadOnly {
		return &RefusedError{Code: CodeReadOnly, msg: fmt.Sprintf(
			"%s: connection %q is read_only; adding and planning migrations need a connection that writes", errPrefix, c.cfg.ConnectionName)}
	}
	return nil
}

// schemaPlan is plan.
func (t *Toolkit) schemaPlan(ctx context.Context, c *conn, in schemaInput) (any, error) {
	if c == nil {
		var err error
		if c, err = t.schemaConn(in.Connection, in.Schema); err != nil {
			return nil, err
		}
	}
	if err := writable(c); err != nil {
		return nil, err
	}
	migrations, err := t.migrations.List(ctx, in.Connection, in.Schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	st, err := t.Status(ctx, in.Connection, in.Schema, migrations)
	if err != nil {
		return nil, err
	}
	pending, drift := st.Pending, st.Drift
	if len(drift) > 0 {
		return nil, &RefusedError{Code: CodeSchemaDrift, msg: fmt.Sprintf("%s: version %d: %s", errPrefix, drift[0].Version, drift[0].Reason)}
	}
	pending = upTo(pending, in.ToVersion)
	plan, err := t.PlanMigrations(ctx, in.Connection, in.Schema, pending)
	if err != nil {
		return nil, err
	}
	return map[string]any{keyConnection: in.Connection, keySchema: in.Schema, "pending": refsOf(pending), "plan": plan, "rolled_back": true}, nil
}

// upTo keeps the migrations at or below to.
func upTo(ms []Migration, to *int) []Migration {
	if to == nil {
		return ms
	}
	return slices.DeleteFunc(slices.Clone(ms), func(m Migration) bool { return m.Version > *to })
}

// schemaChange is apply and down.
func (t *Toolkit) schemaChange(ctx context.Context, c *conn, in schemaInput) (any, error) {
	if err := mayApply(ctx, c); err != nil {
		return nil, err
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout > MaxStatementTimeout {
		return nil, fmt.Errorf("appdb: timeout_seconds is at most %d", int(MaxStatementTimeout.Seconds()))
	}
	migrations, err := t.migrations.List(ctx, in.Connection, in.Schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	o := ApplyOptions{Connection: in.Connection, Schema: in.Schema, Actor: actorOf(ctx), Timeout: timeout}
	event := EventApplied
	if in.Command == schemaDown {
		event = EventReverted
	}
	o.OnDone = func(m Migration, took time.Duration, err error) { t.record(ctx, m, event, took, err) }
	var done []Migration
	if in.Command == schemaApply {
		o.UpTo = in.ToVersion
		done, err = t.Apply(ctx, o, migrations)
	} else {
		to := -1
		if in.ToVersion != nil {
			to = *in.ToVersion
		}
		done, err = t.Revert(ctx, o, migrations, to)
	}
	if err != nil {
		return nil, &doneBeforeError{err: err, done: versionsOf(done)}
	}
	return map[string]any{keyConnection: in.Connection, keySchema: in.Schema, "command": in.Command, "done": refsOf(done)}, nil
}

func versionsOf(ms []Migration) []int {
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = m.Version
	}
	return out
}

// mayApply refuses apply and down to a caller outside the administrators and
// the personas the connection's schema_apply_personas names.
func mayApply(ctx context.Context, c *conn) error {
	if caller, ok := mcpcontext.GetCaller(ctx); ok && caller.IsAdmin {
		return nil
	}
	if persona := mcpcontext.GetPersona(ctx); persona != "" && slices.Contains(c.cfg.SchemaApplyPersonas, persona) {
		return nil
	}
	return &RefusedError{Code: CodeApplyNotAllowed, msg: fmt.Sprintf(
		"%s: applying and reverting migrations on connection %q is for administrators and the personas its "+
			"schema_apply_personas names; add and plan the migration, and ask one of them to apply it", errPrefix, c.cfg.ConnectionName)}
}

// CodeApplyNotAllowed refuses an apply to a caller without the grant.
const CodeApplyNotAllowed = "apply_not_allowed"

// actorOf is who is making the call, as the ledger and the history name them.
func actorOf(ctx context.Context) string {
	caller, ok := mcpcontext.GetCaller(ctx)
	if !ok {
		return ""
	}
	if caller.Email != "" {
		return caller.Email
	}
	return caller.UserID
}

// record appends one event to a migration's history. A failure to record is
// not the command's failure: the change happened, and the ledger in the
// target says so.
func (t *Toolkit) record(ctx context.Context, m Migration, event string, took time.Duration, failure error) {
	e := eventOf(ctx, m, event)
	e.DurationMS = took.Milliseconds()
	if failure != nil {
		e.Event, e.Detail = EventFailed, event+": "+failure.Error()
	}
	t.write(ctx, e)
}

// recordAdded appends an add or edit, with what its review found.
func (t *Toolkit) recordAdded(ctx context.Context, m Migration, event, detail string) {
	e := eventOf(ctx, m, event)
	e.Detail = detail
	t.write(ctx, e)
}

// eventOf is a history entry for m.
func eventOf(ctx context.Context, m Migration, event string) MigrationEvent {
	return MigrationEvent{
		Connection: m.Connection, Schema: m.Schema, Version: m.Version, Event: event,
		Actor: actorOf(ctx), Checksum: m.Checksum,
	}
}

// write stores one history entry.
func (t *Toolkit) write(ctx context.Context, e MigrationEvent) {
	if err := t.migrations.Record(context.WithoutCancel(ctx), e); err != nil {
		slog.WarnContext(ctx, "appdb: recording a migration event failed", "connection", logsan.SanitizeForLog(e.Connection),
			"version", e.Version, "event", e.Event, "error", logsan.SanitizeForLog(err.Error()))
	}
}

// MigrationState is one migration with where it stands in the target, for
// the portal's connection page.
type MigrationState struct {
	Migration
	// State is applied, pending or drift.
	State     string     `json:"state"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	AppliedBy string     `json:"applied_by,omitempty"`
}

// The states a migration is reported in.
const (
	StateApplied = "applied"
	StatePending = "pending"
	StateDrift   = "drift"
)

// SchemaReport is a connection schema's migrations, what the ledger says of
// each, and the history.
type SchemaReport struct {
	Connection     string           `json:"connection"`
	Schema         string           `json:"schema"`
	Ledger         string           `json:"ledger"`
	CurrentVersion int              `json:"current_version"`
	Migrations     []MigrationState `json:"migrations"`
	Drift          []Drift          `json:"drift"`
	Events         []MigrationEvent `json:"events"`
}

// ErrNoMigrationStore is returned where the deployment keeps no migrations.
var ErrNoMigrationStore = errors.New("appdb: this deployment keeps no migrations")

// Report is the portal's view of a connection schema's migrations.
func (t *Toolkit) Report(ctx context.Context, connection, schema string) (*SchemaReport, error) {
	if t.migrations == nil {
		return nil, ErrNoMigrationStore
	}
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return nil, err
	}
	migrations, err := t.migrations.List(ctx, connection, schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	st, err := t.Status(ctx, connection, schema, migrations)
	if err != nil {
		return nil, err
	}
	applied, drift := st.Applied, st.Drift
	events, err := t.migrations.Events(ctx, connection, schema, defaultHistory)
	if err != nil {
		return nil, fmt.Errorf("appdb: reading the history: %w", err)
	}
	return &SchemaReport{
		Connection: connection, Schema: schema, Ledger: c.ledger(), CurrentVersion: currentOf(applied),
		Migrations: statesOf(migrations, applied, drift), Drift: nonNilDrift(drift), Events: events,
	}, nil
}

// currentOf is the highest applied version, 0 for none.
func currentOf(applied []Applied) int {
	current := 0
	for _, a := range applied {
		current = max(current, a.Version)
	}
	return current
}

// statesOf puts each migration beside what the ledger says of it.
func statesOf(migrations []Migration, applied []Applied, drift []Drift) []MigrationState {
	byVersion := make(map[int]Applied, len(applied))
	for _, a := range applied {
		byVersion[a.Version] = a
	}
	drifted := make(map[int]bool, len(drift))
	for _, d := range drift {
		drifted[d.Version] = true
	}
	out := make([]MigrationState, len(migrations))
	for i, m := range migrations {
		s := MigrationState{Migration: m, State: StatePending}
		if a, ok := byVersion[m.Version]; ok {
			at := a.AppliedAt
			s.State, s.AppliedAt, s.AppliedBy = StateApplied, &at, a.AppliedBy
		}
		if drifted[m.Version] {
			s.State = StateDrift
		}
		out[i] = s
	}
	return out
}

// PendingPlan plans a connection schema's pending migrations, for the
// portal's connection page: the migrations planned and the plan.
func (t *Toolkit) PendingPlan(ctx context.Context, connection, schema string) (*Plan, error) {
	if t.migrations == nil {
		return nil, ErrNoMigrationStore
	}
	out, err := t.schemaPlan(ctx, nil, schemaInput{Command: schemaPlan, Connection: connection, Schema: schema})
	if err != nil {
		return nil, err
	}
	res, _ := out.(map[string]any)
	plan, _ := res["plan"].(*Plan)
	return plan, nil
}
