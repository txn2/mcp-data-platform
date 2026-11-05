package postgres

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The review a migration passes before it saves (#2405), as a script's source
// passes internal/platform/scriptsave: the lint, the saved scripts a rename
// would break, an agreed summary for a migration that removes data, and the
// migration set applied from zero on a throwaway database.

// CodeSummaryRequired refuses a migration that removes data without an agreed
// summary of what it removes.
const CodeSummaryRequired = "summary_required"

// ScriptReferences finds the saved scripts that name each of names in the
// statements they run on connection, by name.
type ScriptReferences func(ctx context.Context, connection string, names []string) (map[string][]string, error)

// SetScriptReferences installs the lookup the rename rule reads saved scripts
// through. Without one a rename is not checked against them.
func (t *Toolkit) SetScriptReferences(f ScriptReferences) { t.references = f }

// review lints m, refuses a migration that removes data without an agreed
// summary, and applies every migration its schema would hold from zero.
// It returns what the lint found.
func (t *Toolkit) review(ctx context.Context, in schemaInput, m Migration) ([]Finding, error) {
	target, unread := t.lintTarget(ctx, m.Connection, m.Schema)
	findings, renames := Lint(m.Up, target)
	if unread != nil {
		findings = append(findings, *unread)
	}
	if r := Refused(findings); len(r) > 0 {
		return nil, statementRefusal(m.Version, "", r)
	}
	// The down SQL is held to the same refusals (#2475): it is stored,
	// exported and run like the up SQL, so a credential or a superuser
	// statement in it is kept and reached the same way. Its destructive
	// findings are what a revert is for, and are not asked about.
	if downFindings, _ := Lint(m.Down, Target{}); len(Refused(downFindings)) > 0 {
		return nil, statementRefusal(m.Version, "down ", Refused(downFindings))
	}
	findings = append(findings, t.renameFindings(ctx, m.Connection, renames)...)
	if d := Destructive(findings); len(d) > 0 && !agreed(in) {
		return nil, summaryRefusal(m.Version, d)
	}
	held, err := t.migrations.List(ctx, m.Connection, m.Schema)
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	if err := t.CheckFromZero(ctx, m.Connection, m.Schema, withMigration(held, m)); err != nil {
		var refused *RefusedError
		if !errors.As(err, &refused) || refused.Code != CodeCreateDBRequired {
			return nil, withServerFindings(err, findings)
		}
		findings = append(findings, Finding{
			Rule: RuleFromZeroUnchecked, Severity: SeverityWarning,
			Effect: "the migrations were not applied from zero on a throwaway database: " + refused.msg,
			Safe:   "grant the test server's user CREATEDB, or set the connection's test_connection",
		})
	}
	return findings, nil
}

// CodeStatementRefused refuses a migration holding a statement that needs a
// privilege an application's role must not hold, or reaches outside the
// database (#2449).
const CodeStatementRefused = "statement_refused"

// lintTargetSQL reads what the version and row-security rules need of the
// connection's server and schema, in one round trip.
const lintTargetSQL = `SELECT current_setting('server_version_num')::int / 10000,
COALESCE(ARRAY(SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = $1 AND c.relrowsecurity), '{}'),
COALESCE(ARRAY(SELECT c.relname || '.' || a.attname FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND a.attgenerated = 'v' AND NOT a.attisdropped), '{}')`

// lintTarget reads the server's major, the schema's tables with row-level
// security and its virtual generated columns from the connection. When they
// cannot be read the review goes on without them and says so.
func (t *Toolkit) lintTarget(ctx context.Context, connection, schema string) (Target, *Finding) {
	c, ok := t.lookup(connection)
	if !ok {
		return Target{}, nil
	}
	var target Target
	var rls, virtual []string
	if err := c.reader().QueryRow(ctx, lintTargetSQL, schema).Scan(&target.Major, &rls, &virtual); err != nil {
		return Target{}, &Finding{
			Rule: ruleTargetUnread, Severity: SeverityWarning, Object: connection,
			Effect: "the server's version and the schema's row-level security could not be read, so the rules that need them " +
				"were not checked: " + scrubDriverError(err),
			Safe: "add the migration again once the connection answers",
		}
	}
	target.RowSecurity = map[string]bool{}
	for _, table := range rls {
		target.RowSecurity[table] = true
	}
	target.Virtual = map[string]map[string]bool{}
	for _, tc := range virtual {
		table, col, _ := strings.Cut(tc, ".")
		if target.Virtual[table] == nil {
			target.Virtual[table] = map[string]bool{}
		}
		target.Virtual[table][col] = true
	}
	return target, nil
}

// statementRefusal names each refused statement and why.
func statementRefusal(version int, half string, refused []Finding) error {
	parts := make([]string, len(refused))
	for i, f := range refused {
		parts[i] = fmt.Sprintf("%sstatement %d (%s) %s", half, f.Statement, f.Object, f.Effect)
	}
	return &RefusedError{Code: CodeStatementRefused, msg: fmt.Sprintf(
		"%s: migration %d is refused: %s. An application's migration runs as the application's role, which must not hold "+
			"the privileges these need", errPrefix, version, strings.Join(parts, "; "))}
}

// withServerFindings adds to a from-zero failure what the lint found the
// server's major refuses (a feature it lacks, an index on a virtual column):
// on that server the apply fails on them, and the failure alone does not say
// why.
func withServerFindings(err error, findings []Finding) error {
	var refused *RefusedError
	if !errors.As(err, &refused) {
		return err
	}
	var parts []string
	for _, f := range findings {
		if f.Rule == ruleNeedsNewerServer || f.Rule == ruleIndexVirtualColumn {
			parts = append(parts, fmt.Sprintf("statement %d %s (instead: %s)", f.Statement, f.Effect, f.Safe))
		}
	}
	if len(parts) == 0 {
		return err
	}
	return &RefusedError{Code: refused.Code, msg: refused.msg + ". The review found: " + strings.Join(parts, "; ")}
}

// agreed reports whether an add carries a summary the person agreed to.
func agreed(in schemaInput) bool {
	return strings.TrimSpace(in.ChangeSummary) != "" && in.UserAgreed
}

// summaryRefusal names what a migration removes and how to save it.
func summaryRefusal(version int, destructive []Finding) error {
	parts := make([]string, len(destructive))
	for i, f := range destructive {
		parts[i] = fmt.Sprintf("statement %d %s", f.Statement, f.Effect)
	}
	return &RefusedError{Code: CodeSummaryRequired, msg: fmt.Sprintf(
		"%s: migration %d removes data: %s. Tell the person the application is built for what it removes, in plain "+
			"words, and once they agree, add it again with change_summary (what it removes) and user_agreed=true",
		errPrefix, version, strings.Join(parts, "; "))}
}

// renameFindings warns on each rename a saved script names.
func (t *Toolkit) renameFindings(ctx context.Context, connection string, renames []Rename) []Finding {
	if t.references == nil || len(renames) == 0 {
		return nil
	}
	names := make([]string, 0, len(renames))
	for _, r := range renames {
		if !slices.Contains(names, r.Name()) {
			names = append(names, r.Name())
		}
	}
	refs, err := t.references(ctx, connection, names)
	if err != nil {
		slog.WarnContext(ctx, "appdb: reading the scripts a rename would break failed", "connection", logsan.SanitizeForLog(connection),
			"error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	var out []Finding
	for _, r := range renames {
		scripts := refs[r.Name()]
		if len(scripts) == 0 {
			continue
		}
		out = append(out, Finding{
			Rule: RuleRenameReferenced, Severity: SeverityWarning, Statement: r.Statement,
			Object: r.Object(), Scripts: scripts,
			Effect: fmt.Sprintf("renames %s, which %s name in the statements they run; they fail once the migration applies",
				r.Object(), strings.Join(scripts, ", ")),
			Safe: "add the new name beside the old one (a new column kept in step, or a view), move the scripts to it, " +
				"and remove the old name in a later migration",
		})
	}
	return out
}

// withMigration is the connection's migrations with m added, or in place of
// the one it replaces, in schema and version order.
func withMigration(all []Migration, m Migration) []Migration {
	out := slices.DeleteFunc(slices.Clone(all), func(have Migration) bool {
		return have.Schema == m.Schema && have.Version == m.Version
	})
	out = append(out, m)
	slices.SortFunc(out, func(a, b Migration) int {
		return cmp.Or(cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Version, b.Version))
	})
	return out
}

// reviewDetail is what a migration's history records of its review: the
// summary and who agreed to it, and the findings.
func reviewDetail(in schemaInput, actor string, findings []Finding) string {
	var parts []string
	if agreed(in) {
		parts = append(parts, fmt.Sprintf("change summary agreed by %s: %s", cmp.Or(actor, "the caller"), strings.TrimSpace(in.ChangeSummary)))
	}
	for _, f := range findings {
		p := f.Rule
		if f.Object != "" {
			p += " on " + f.Object
		}
		if f.Statement > 0 {
			p += fmt.Sprintf(" (statement %d)", f.Statement)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}
