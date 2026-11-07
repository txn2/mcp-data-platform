//nolint:revive // max-public-structs: one cohesive description of a schema (the server, its extensions and each kind of object describe reports), not a heap of unrelated types.
package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The objects describe reports beside tables (#2447): what the server is,
// the extensions it holds and offers, and every function, trigger, view,
// policy, enum, domain and partition the applied migrations produced. An
// agent can build on only what it can read back, and a policy (#2403) or an
// outbox trigger (#2408) was invisible once applied. Each list is scoped to
// the schema describe was asked for, except the extensions, which are the
// database's; an object an extension owns is left out, since a migration did
// not write it.

// Server is the server's version: Major is what the version rules of a
// migration's review are read against.
type Server struct {
	Major   int    `json:"major"`
	Version string `json:"version"`
}

// Extension is an extension installed in the database.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Schema  string `json:"schema"`
}

// AvailableExtension is an extension the server offers that the database
// has not installed.
type AvailableExtension struct {
	Name           string `json:"name"`
	DefaultVersion string `json:"default_version"`
	Comment        string `json:"comment,omitempty"`
}

// Function is a function or a procedure.
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Returns   string `json:"returns,omitempty"`
	// Kind is function or procedure.
	Kind     string `json:"kind"`
	Language string `json:"language"`
	// Volatility is immutable, stable or volatile.
	Volatility      string `json:"volatility"`
	SecurityDefiner bool   `json:"security_definer"`
	// SearchPath is the search_path the function pins with SET, empty when
	// it pins none.
	SearchPath string `json:"search_path,omitempty"`
	Body       string `json:"body"`
}

// Trigger is a trigger on one of the schema's tables.
type Trigger struct {
	Name string `json:"name"`
	// Timing is before, after or instead_of.
	Timing string `json:"timing"`
	// Events are insert, update, delete and truncate, in that order.
	Events []string `json:"events"`
	// Level is row or statement.
	Level    string `json:"level"`
	When     string `json:"when,omitempty"`
	Function string `json:"function"`
	// Enabled is enabled, disabled, replica or always (session_replication_role).
	Enabled    string `json:"enabled"`
	Definition string `json:"definition"`
}

// View is what a view or materialized view is defined as.
type View struct {
	Definition      string `json:"definition"`
	SecurityInvoker bool   `json:"security_invoker"`
	SecurityBarrier bool   `json:"security_barrier"`
	// CheckOption is local or cascaded, empty when the view has none.
	CheckOption string `json:"check_option,omitempty"`
}

// Policy is a row-level security policy on a table.
type Policy struct {
	Name string `json:"name"`
	// Command is ALL, SELECT, INSERT, UPDATE or DELETE.
	Command string   `json:"command"`
	Roles   []string `json:"roles"`
	// Permissive is false on a restrictive policy.
	Permissive bool   `json:"permissive"`
	Using      string `json:"using,omitempty"`
	WithCheck  string `json:"with_check,omitempty"`
}

// Partition is one partition of a partitioned table and its bound.
type Partition struct {
	Name  string `json:"name"`
	Bound string `json:"bound"`
}

// Enum is an enum type and its values in their order.
type Enum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Domain is a domain over a base type and its checks.
type Domain struct {
	Name     string   `json:"name"`
	BaseType string   `json:"base_type"`
	Nullable bool     `json:"nullable"`
	Default  string   `json:"default,omitempty"`
	Checks   []string `json:"checks"`
}

const describeServerSQL = `/* mcp-data-platform */ SELECT current_setting('server_version'), current_setting('server_version_num')::int / 10000`

const describeExtensionsSQL = `/* mcp-data-platform */ SELECT e.extname, e.extversion, n.nspname
FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace ORDER BY e.extname`

const describeAvailableSQL = `/* mcp-data-platform */ SELECT name, COALESCE(default_version, ''), COALESCE(comment, '')
FROM pg_available_extensions WHERE installed_version IS NULL ORDER BY name`

const describeFunctionsSQL = `/* mcp-data-platform */ SELECT p.proname, pg_get_function_identity_arguments(p.oid),
COALESCE(pg_get_function_result(p.oid), ''), p.prokind::text, l.lanname, p.provolatile::text, p.prosecdef,
COALESCE((SELECT substr(cfg, 13) FROM unnest(p.proconfig) cfg WHERE cfg LIKE 'search_path=%' LIMIT 1), ''),
COALESCE(NULLIF(p.prosrc, ''), pg_get_function_sqlbody(p.oid), '')
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
WHERE n.nspname = $1 AND p.prokind IN ('f', 'p')
AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
ORDER BY p.proname, 2`

const describeTriggersSQL = `/* mcp-data-platform */ SELECT c.relname, t.tgname, t.tgtype::int, t.tgenabled::text, fn.nspname || '.' || p.proname,
COALESCE(substring(pg_get_triggerdef(t.oid) FROM 'WHEN \((.*)\) EXECUTE'), ''), pg_get_triggerdef(t.oid)
FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_proc p ON p.oid = t.tgfoid JOIN pg_namespace fn ON fn.oid = p.pronamespace
WHERE n.nspname = $1 AND NOT t.tgisinternal ORDER BY c.relname, t.tgname`

const describeViewsSQL = `/* mcp-data-platform */ SELECT c.relname, pg_get_viewdef(c.oid, true), COALESCE(c.reloptions, '{}')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('v', 'm') ORDER BY c.relname`

const describePoliciesSQL = `/* mcp-data-platform */ SELECT tablename, policyname, cmd, COALESCE(roles::text[], '{}'), permissive = 'PERMISSIVE',
COALESCE(qual, ''), COALESCE(with_check, '')
FROM pg_policies WHERE schemaname = $1 ORDER BY tablename, policyname`

const describePartitionsSQL = `/* mcp-data-platform */ SELECT parent.relname,
CASE WHEN cn.nspname = $1 THEN child.relname ELSE cn.nspname || '.' || child.relname END,
pg_get_expr(child.relpartbound, child.oid)
FROM pg_inherits i JOIN pg_class parent ON parent.oid = i.inhparent JOIN pg_class child ON child.oid = i.inhrelid
JOIN pg_namespace n ON n.oid = parent.relnamespace JOIN pg_namespace cn ON cn.oid = child.relnamespace
WHERE n.nspname = $1 AND parent.relkind = 'p' ORDER BY parent.relname, 2`

const describeEnumsSQL = `/* mcp-data-platform */ SELECT t.typname, e.enumlabel
FROM pg_type t JOIN pg_enum e ON e.enumtypid = t.oid JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = $1 AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')
ORDER BY t.typname, e.enumsortorder`

const describeDomainsSQL = `/* mcp-data-platform */ SELECT t.typname, format_type(t.typbasetype, t.typtypmod), NOT t.typnotnull, COALESCE(t.typdefault, ''),
COALESCE(ARRAY(SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c WHERE c.contypid = t.oid AND c.contype = 'c' ORDER BY c.conname), '{}')
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = $1 AND t.typtype = 'd' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')
ORDER BY t.typname`

// Words describe reports the catalog's codes as.
var (
	functionKinds = map[string]string{"f": kwFunction, "p": kwProcedure}
	volatilities  = map[string]string{"i": "immutable", "s": "stable", "v": "volatile"}
	triggerStates = map[string]string{"O": "enabled", "D": "disabled", "R": "replica", "A": "always"}
)

// pg_trigger.tgtype bits.
const (
	tgRow      = 1 << 0
	tgBefore   = 1 << 1
	tgInsert   = 1 << 2
	tgDelete   = 1 << 3
	tgUpdate   = 1 << 4
	tgTruncate = 1 << 5
	tgInstead  = 1 << 6
)

// triggerShape reads a trigger's timing, events and level from its tgtype.
func triggerShape(tgtype int) (timing string, events []string, level string) {
	switch {
	case tgtype&tgInstead != 0:
		timing = "instead_of"
	case tgtype&tgBefore != 0:
		timing = "before"
	default:
		timing = "after"
	}
	events = []string{}
	for _, e := range []struct {
		bit  int
		name string
	}{{tgInsert, "insert"}, {tgUpdate, "update"}, {tgDelete, "delete"}, {tgTruncate, "truncate"}} {
		if tgtype&e.bit != 0 {
			events = append(events, e.name)
		}
	}
	level = "statement"
	if tgtype&tgRow != 0 {
		level = "row"
	}
	return timing, events, level
}

// viewOf reads a view's options from its reloptions ("security_invoker=true").
func viewOf(definition string, options []string) *View {
	v := &View{Definition: definition}
	for _, o := range options {
		key, value, _ := strings.Cut(o, "=")
		on := optionTrue[value]
		switch key {
		case "security_invoker":
			v.SecurityInvoker = on
		case "security_barrier":
			v.SecurityBarrier = on
		case "check_option":
			v.CheckOption = value
		}
	}
	return v
}

// describeServer reads the server's version and the database's extensions.
func describeServer(ctx context.Context, tx pgx.Tx, d *Description) error {
	if err := tx.QueryRow(ctx, describeServerSQL).Scan(&d.Server.Version, &d.Server.Major); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if err := scanRows(ctx, tx, describeExtensionsSQL, nil, func(rows pgx.Rows) error {
		var e Extension
		if err := rows.Scan(&e.Name, &e.Version, &e.Schema); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		d.Extensions = append(d.Extensions, e)
		return nil
	}); err != nil {
		return err
	}
	return scanRows(ctx, tx, describeAvailableSQL, nil, func(rows pgx.Rows) error {
		var e AvailableExtension
		if err := rows.Scan(&e.Name, &e.DefaultVersion, &e.Comment); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		d.AvailableExtensions = append(d.AvailableExtensions, e)
		return nil
	})
}

// describeTableObjects reads the triggers, view definitions, policies and
// partitions into d's tables.
func describeTableObjects(ctx context.Context, tx pgx.Tx, schema string, d *Description, index map[string]int) error {
	at := func(table string) *Table {
		if i, ok := index[table]; ok {
			return &d.Tables[i]
		}
		return nil
	}
	steps := []struct {
		sql  string
		each func(pgx.Rows) error
	}{
		{describeTriggersSQL, func(rows pgx.Rows) error { return scanTrigger(rows, at) }},
		{describeViewsSQL, func(rows pgx.Rows) error { return scanView(rows, at) }},
		{describePoliciesSQL, func(rows pgx.Rows) error { return scanPolicy(rows, at) }},
		{describePartitionsSQL, func(rows pgx.Rows) error { return scanPartition(rows, at) }},
	}
	for _, s := range steps {
		if err := scanAll(ctx, tx, s.sql, schema, s.each); err != nil {
			return err
		}
	}
	return nil
}

// tableAt finds a described table by name, nil when describe did not list it.
type tableAt func(table string) *Table

func scanTrigger(rows pgx.Rows, at tableAt) error {
	var table, state string
	var tr Trigger
	var tgtype int
	if err := rows.Scan(&table, &tr.Name, &tgtype, &state, &tr.Function, &tr.When, &tr.Definition); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	tr.Timing, tr.Events, tr.Level = triggerShape(tgtype)
	tr.Enabled = triggerStates[state]
	if tb := at(table); tb != nil {
		tb.Triggers = append(tb.Triggers, tr)
	}
	return nil
}

func scanView(rows pgx.Rows, at tableAt) error {
	var table, definition string
	var options []string
	if err := rows.Scan(&table, &definition, &options); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if tb := at(table); tb != nil {
		tb.View = viewOf(definition, options)
	}
	return nil
}

func scanPolicy(rows pgx.Rows, at tableAt) error {
	var table string
	var p Policy
	if err := rows.Scan(&table, &p.Name, &p.Command, &p.Roles, &p.Permissive, &p.Using, &p.WithCheck); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if p.Roles == nil {
		p.Roles = []string{}
	}
	if tb := at(table); tb != nil {
		tb.Policies = append(tb.Policies, p)
	}
	return nil
}

func scanPartition(rows pgx.Rows, at tableAt) error {
	var table string
	var p Partition
	if err := rows.Scan(&table, &p.Name, &p.Bound); err != nil {
		return err //nolint:wrapcheck // read by its type
	}
	if tb := at(table); tb != nil {
		tb.Partitions = append(tb.Partitions, p)
	}
	return nil
}

// describeTypes reads the schema's functions, enums and domains into d.
func describeTypes(ctx context.Context, tx pgx.Tx, schema string, d *Description) error {
	if err := scanAll(ctx, tx, describeFunctionsSQL, schema, func(rows pgx.Rows) error {
		var f Function
		var kind, volatility string
		if err := rows.Scan(&f.Name, &f.Arguments, &f.Returns, &kind, &f.Language, &volatility,
			&f.SecurityDefiner, &f.SearchPath, &f.Body); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		f.Kind, f.Volatility = functionKinds[kind], volatilities[volatility]
		d.Functions = append(d.Functions, f)
		return nil
	}); err != nil {
		return err
	}
	if err := scanAll(ctx, tx, describeEnumsSQL, schema, func(rows pgx.Rows) error {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		if n := len(d.Enums); n > 0 && d.Enums[n-1].Name == name {
			d.Enums[n-1].Values = append(d.Enums[n-1].Values, value)
			return nil
		}
		d.Enums = append(d.Enums, Enum{Name: name, Values: []string{value}})
		return nil
	}); err != nil {
		return err
	}
	return scanAll(ctx, tx, describeDomainsSQL, schema, func(rows pgx.Rows) error {
		var dm Domain
		if err := rows.Scan(&dm.Name, &dm.BaseType, &dm.Nullable, &dm.Default, &dm.Checks); err != nil {
			return err //nolint:wrapcheck // read by its type
		}
		if dm.Checks == nil {
			dm.Checks = []string{}
		}
		d.Domains = append(d.Domains, dm)
		return nil
	})
}
