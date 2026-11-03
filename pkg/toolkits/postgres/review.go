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
	findings, renames := Lint(m.Up)
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
			return nil, err
		}
		findings = append(findings, Finding{
			Rule: RuleFromZeroUnchecked, Severity: SeverityWarning,
			Effect: "the migrations were not applied from zero on a throwaway database: " + refused.msg,
			Safe:   "grant the test server's user CREATEDB, or set the connection's test_connection",
		})
	}
	return findings, nil
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
