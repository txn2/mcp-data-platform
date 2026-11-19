package appsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appprepare"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Each environment its own schema (#2709). An application holding a schema
// runs every environment against a schema of its own on the application's
// connection, logged in as a run role granted on that schema alone, so a dev
// run, a dev site and an agent acting in dev never read or write prod's rows:
// the database refuses them. The application's migrations are kept once,
// under its own schema, and each environment's schema applies them: a release
// to the first environment's, a promote to the target's, up to what the
// promoted release recorded. A new environment's schema is built from the
// structure of the last environment's and recorded as having applied what
// that one had. An environment that existed before #2709 runs in the
// application's schema, so nothing moves on upgrade.

// maxEnvSchemaBytes is the longest schema an environment is given: its run
// role's name is the schema followed by "_run_" and eight hex digits, within
// PostgreSQL's 63 bytes.
const maxEnvSchemaBytes = 50

// schemaPattern is a schema an environment may be given: a lowercase
// identifier, never quoted.
var schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// DefaultSchema is the schema an environment added with none named runs in:
// the application's schema, an underscore, and the environment's name with
// each hyphen an underscore.
func DefaultSchema(appSchema, env string) string {
	return appSchema + "_" + strings.ReplaceAll(env, "-", "_")
}

// envSchema is the schema e runs in: its own, or the application's for one
// that names none.
func envSchema(a *application.Application, e *application.Environment) string {
	if e == nil || e.Schema == "" {
		return a.Schema
	}
	return e.Schema
}

// schemaTarget is where e's migrations apply.
func schemaTarget(a *application.Application, e *application.Environment) appschema.Target {
	return appschema.Target{Connection: a.Connection, Source: a.Schema, Schema: envSchema(a, e)}
}

// exclusive reports whether no environment of a but the one at i runs in its
// schema. A rollback reverts the migrations of a schema only its own
// environment runs in: one environments share (as every environment of an
// application did before #2709) is left alone, as it was then.
func exclusive(a *application.Application, i int) bool {
	if i < 0 || i >= len(a.Environments) {
		return len(a.Environments) == 0
	}
	schema := envSchema(a, &a.Environments[i])
	for j := range a.Environments {
		if j != i && envSchema(a, &a.Environments[j]) == schema {
			return false
		}
	}
	return true
}

// usedBy is the environment of a running in schema, "" for none.
func usedBy(a *application.Application, schema string) string {
	for i := range a.Environments {
		if envSchema(a, &a.Environments[i]) == schema {
			return a.Environments[i].Name
		}
	}
	return ""
}

// newEnvironmentSchema gives e, being added to a, its own schema (#2709):
// the one it names or DefaultSchema, built from the last environment's
// structure on the application's connection, with that environment's
// migrations recorded as applied and a run role granted on it alone. An
// application holding no schema gives it none.
func (s *Service) newEnvironmentSchema(ctx context.Context, actor application.Actor, a *application.Application, e *application.Environment) error {
	if a.Schema == "" {
		if e.Schema != "" {
			return refused("application %q holds no schema, so environment %s cannot be given one; add the application's schema first", a.Name, e.Name)
		}
		return nil
	}
	schema := e.Schema
	if schema == "" {
		schema = DefaultSchema(a.Schema, e.Name)
	}
	if err := s.mayBuild(ctx, a, schema); err != nil {
		return err
	}
	last := a.Last()
	from := envSchema(a, last)
	st, err := s.Schema.StateIn(ctx, schemaTarget(a, last))
	if err != nil {
		return refused("environment %s was not added: reading where schema %s's migrations stand: %s", e.Name, from, err.Error())
	}
	if err := s.Roles.CloneSchema(ctx, a.Connection, from, schema); err != nil {
		return refused("environment %s was not added: building schema %s from %s: %s", e.Name, schema, from, err.Error())
	}
	at := appschema.Target{Connection: a.Connection, Source: a.Schema, Schema: schema}
	if err := s.Schema.Baseline(ctx, at, actorName(actor, a), st.Applied); err != nil {
		return fmt.Errorf("environment %s was not added; schema %s was built and is left for an administrator to drop: %w", e.Name, schema, err)
	}
	held := application.HeldSchema{Connection: a.Connection, Schema: schema}
	if err := s.newRunRole(ctx, a.ID, &held); err != nil {
		return fmt.Errorf("environment %s was not added; schema %s was built and is left for an administrator to drop: %w", e.Name, schema, err)
	}
	e.Schema, e.Held = schema, &held
	return nil
}

// mayBuild refuses building schema for a new environment of a: one it cannot
// be given, a deployment that cannot seal its run role's password, or an
// application not prepared for environments of their own.
func (s *Service) mayBuild(ctx context.Context, a *application.Application, schema string) error {
	if err := s.schemaFree(ctx, a, schema); err != nil {
		return err
	}
	if err := s.canSeal(); err != nil {
		return err
	}
	return s.prepared(ctx, a)
}

// schemaFree refuses a schema a new environment cannot be given: not a
// lowercase identifier, too long for its run role's name, one of the
// application's environments' already, or another application's.
func (s *Service) schemaFree(ctx context.Context, a *application.Application, schema string) error {
	if !schemaPattern.MatchString(schema) {
		return refused("schema %q: a lowercase identifier (letters, digits and underscores)", schema)
	}
	if len(schema) > maxEnvSchemaBytes {
		return refused("schema %q is %d bytes; an environment's schema is at most %d, so its run role's name fits; name a shorter one", schema, len(schema), maxEnvSchemaBytes)
	}
	if env := usedBy(a, schema); env != "" {
		return refused("schema %s is environment %s's; each new environment runs in a schema of its own", schema, env)
	}
	other, err := s.Store.Holding(ctx, a.Connection, schema)
	if err == nil {
		return fmt.Errorf("schema %s belongs to application %q: %w", application.SchemaPartID(a.Connection, schema), other.Name, application.ErrSchemaHeld)
	}
	if !errors.Is(err, application.ErrNotFound) {
		return err //nolint:wrapcheck // the store's errors are read by identity
	}
	return nil
}

// forgetEnvironmentSchema drops the run role of the schema a removed
// environment ran in, when no other environment runs in it and it is not the
// application's own; the schema itself is kept until an administrator drops
// it. A failure is logged: the role is left, and logs in to nothing the
// platform hands out.
func (s *Service) forgetEnvironmentSchema(ctx context.Context, a *application.Application, removed *application.Environment) {
	schema := envSchema(a, removed)
	if s.Roles == nil || schema == "" || schema == a.Schema {
		return
	}
	for i := range a.Environments {
		if a.Environments[i].Name != removed.Name && envSchema(a, &a.Environments[i]) == schema {
			return
		}
	}
	if err := s.Roles.DropRunRole(ctx, a.Connection, schema); err != nil {
		slog.WarnContext(ctx, "application: dropping a removed environment's run role failed",
			"application", logsan.SanitizeForLog(a.Name), "schema", logsan.SanitizeForLog(schema),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// applyToEnvironment applies the application's migrations to the schema e
// runs in, through version upTo (appschema.AllVersions for every pending
// one), and grants its run role what they made.
func (s *Service) applyToEnvironment(ctx context.Context, actor application.Actor, a *application.Application, e *application.Environment, upTo int) (*appschema.Applied, error) {
	at := schemaTarget(a, e)
	applied, err := s.Schema.ApplyIn(ctx, at, actorName(actor, a), upTo)
	if err != nil {
		return nil, err //nolint:wrapcheck // each caller says what was not done
	}
	s.grantRunRole(ctx, a, at.Schema)
	return applied, nil
}

// revertEnvironment reverts the migrations of the schema the environment at
// i runs in above version to, when that schema is its own; ok is false when
// it shares the schema and nothing was reverted.
func (s *Service) revertEnvironment(ctx context.Context, actor application.Actor, a *application.Application, i, to int) (rev *appschema.Reverted, ok bool, err error) {
	if a.Schema == "" || !exclusive(a, i) {
		return nil, false, nil
	}
	var e *application.Environment
	if i >= 0 && i < len(a.Environments) {
		e = &a.Environments[i]
	}
	rev, err = s.Schema.RollbackIn(ctx, schemaTarget(a, e), actorName(actor, a), max(to, a.SchemaAdoptedVersion))
	return rev, err == nil, err //nolint:wrapcheck // each caller says what was not done
}

// Preparer is what prepare does to an application (appprepare.Preparer).
type Preparer interface {
	Named(ctx context.Context, a *application.Application) (appprepare.Counts, error)
	Plan(ctx context.Context, a *application.Application, caller scriptsave.Caller) (*appprepare.Plan, error)
	Save(ctx context.Context, a *application.Application, caller scriptsave.Caller, author script.Author) (*appprepare.Plan, error)
}

// prepared refuses a new environment while anything in a names its schema
// or its roles (#2709): such SQL would reach another environment's data from
// the new one. A deployment with no preparer has nothing to count with and
// refuses nothing.
func (s *Service) prepared(ctx context.Context, a *application.Application) error {
	if s.Preparer == nil {
		return nil
	}
	c, err := s.Preparer.Named(ctx, a)
	if err != nil {
		return fmt.Errorf("reading what application %q names: %w", a.Name, err)
	}
	if c.Total() == 0 {
		return nil
	}
	return refused("application %q is not ready for an environment of its own: %d scripts, %d libraries and %d pending "+
		"migrations name schema %s or its roles, which from a new environment would reach this one's data. Prepare the "+
		"application first (manage_application command prepare, or Prepare on the application's page): it removes them, "+
		"replays each changed script's recent runs to show the answers are the same, and saves new versions to release",
		a.Name, c.Scripts, c.Libraries, c.Migrations, a.Schema)
}

// errPrepareNeedsAdmin refuses saving prepare's changes to anyone but an
// administrator, who adds the environment they prepare for.
var errPrepareNeedsAdmin = fmt.Errorf("saving prepare's changes is for administrators: %w", application.ErrNotAllowed)

// Prepare is what prepare changes in the application ref names (#2709):
// read by its owner and administrators, saved by an administrator when save
// is set. The save gate refusing any file saves nothing.
func (s *Service) Prepare(ctx context.Context, actor application.Actor, ref string, save bool) (*appprepare.Plan, error) {
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !a.MayEdit(actor) {
		return nil, fmt.Errorf("prepare is the application's owner's and administrators': %w", application.ErrNotAllowed)
	}
	if s.Preparer == nil {
		return nil, refused("this deployment cannot prepare applications")
	}
	caller := scriptsave.Caller{Email: actor.Email, Admin: actor.Admin, Roles: slices.Clone(actor.Roles)}
	if !save {
		return s.Preparer.Plan(ctx, a, caller) //nolint:wrapcheck // the preparer says what it read
	}
	if !actor.Admin {
		return nil, errPrepareNeedsAdmin
	}
	plan, err := s.Preparer.Save(ctx, a, caller, script.Author{Email: actor.Email, Roles: slices.Clone(actor.Roles), Subject: actor.Subject})
	if errors.Is(err, appprepare.ErrNotReady) {
		return plan, &RefusedError{Msg: err.Error()}
	}
	return plan, err //nolint:wrapcheck // the preparer says what it saved
}
