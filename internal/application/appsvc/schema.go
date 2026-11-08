package appsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
)

// Roles creates, grants and drops an application's run role on its
// connection (#2539): the postgres toolkit.
type Roles interface {
	CreateRunRole(ctx context.Context, connection, schema string) (role, password string, err error)
	GrantRunRole(ctx context.Context, connection, schema string) (string, error)
	DropRunRole(ctx context.Context, connection, schema string) error
}

// Sealer seals a run role's password for storage, bound to what it is the
// password of, and opens it again: the platform's field encryptor.
type Sealer interface {
	Enabled() bool
	SealBound(binding, plaintext string) (string, error)
	OpenBound(binding, sealed string) (string, error)
}

// RunRoleBinding is what a run role's sealed password is bound to: the
// application, the connection and schema it holds, and the role. A sealed
// value opens only for the same four.
func RunRoleBinding(applicationID, connection, schema, role string) string {
	return "application-run-role:" + applicationID + ":" + connection + "/" + schema + ":" + role
}

// SchemaView is the schema part as an application's page and get show it
// (#2539): where its migrations stand, what the current release recorded,
// and the run role its scripts log in as.
type SchemaView struct {
	Connection string `json:"connection" example:"orders"`
	Schema     string `json:"schema" example:"shop"`
	// MigrationVersion is the highest migration applied in the database;
	// ReleasedVersion the one the current release recorded; Pending the
	// versions a release would apply.
	MigrationVersion int   `json:"migration_version" example:"7"`
	ReleasedVersion  int   `json:"released_version" example:"6"`
	Pending          []int `json:"pending"`
	// AdoptedVersion is the version applied when the schema was added; a
	// rollback reverts nothing at or below it.
	AdoptedVersion int `json:"adopted_version" example:"0"`
	// RunRole is the role the application's scripts log in as, empty when
	// it has none; WriteRole the role it is granted, empty while the
	// schema's migrations define none.
	RunRole   string `json:"run_role" example:"shop_run"`
	WriteRole string `json:"write_role" example:"shop_write"`
	// Note says what the run role still needs, empty when nothing.
	Note string `json:"note,omitempty"`
	// Problem says the migrations could not be read, empty when they were.
	Problem string `json:"problem,omitempty"`
}

// reachable is the application database connections the actor may name.
func (s *Service) reachable(ctx context.Context, actor application.Actor) ([]string, error) {
	if s.Reach == nil {
		return nil, refused("this deployment has no application database connection to hold a schema on")
	}
	conns, err := s.Reach(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("listing the application database connections you reach: %w", err)
	}
	slices.Sort(conns)
	return conns, nil
}

// chooseConnection is the connection a schema is added on: the one named,
// which the actor must reach, or the only one the actor reaches.
func (s *Service) chooseConnection(ctx context.Context, actor application.Actor, named, schema string) (string, error) {
	conns, err := s.reachable(ctx, actor)
	if err != nil {
		return "", err
	}
	switch {
	case named != "" && slices.Contains(conns, named):
		return named, nil
	case len(conns) == 0:
		return "", refused("you reach no application database connection (kind appdb), so no schema can be added; "+
			"an administrator configures the connection and gives your persona access to it%s", namedSuffix(named))
	case named != "":
		return "", refused("connection %q is not an application database connection (kind appdb) you reach; "+
			"name one of: %s, as part_id \"<connection>/%s\"", named, strings.Join(conns, ", "), schema)
	case len(conns) > 1:
		return "", refused("you reach several application database connections; name one as part_id "+
			"\"<connection>/%s\": %s", schema, strings.Join(conns, ", "))
	}
	return conns[0], nil
}

func namedSuffix(named string) string {
	if named == "" {
		return ""
	}
	return fmt.Sprintf(" (%q is not one)", named)
}

// addSchema makes the application hold a schema (#2539): on an existing
// application database connection the actor reaches, a schema no other
// application holds, with the migration version it already has adopted and a
// run role created for its scripts. Adding the schema it already holds gives
// it a run role when it has none, which an application created before run
// roles existed lacks.
func (s *Service) addSchema(ctx context.Context, actor application.Actor, a *application.Application, id string) (*application.Application, error) {
	named, schema := application.ParseSchemaPartID(id)
	if schema == "" {
		return nil, refused("part_id names the schema: \"<connection>/<schema>\", or the schema alone")
	}
	if a.Schema != "" {
		if a.Schema == schema && (named == "" || named == a.Connection) {
			return s.repairRunRole(ctx, a)
		}
		return nil, refused("application %q holds schema %s already, and an application holds one schema; "+
			"remove it first with remove_part", a.Name, application.SchemaPartID(a.Connection, a.Schema))
	}
	held, err := s.schemaToHold(ctx, actor, named, schema)
	if err != nil {
		return nil, err
	}
	conn := held.Connection
	if err := s.newRunRole(ctx, a.ID, &held); err != nil {
		return nil, err
	}
	if err := s.Store.SetSchema(ctx, a.ID, held); err != nil {
		s.dropRunRole(ctx, conn, schema)
		if errors.Is(err, application.ErrSchemaHeld) {
			return nil, s.schemaTaken(ctx, conn, schema)
		}
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// schemaToHold is the schema an application takes on: on the connection
// chosen, held by no other application, with the version it has applied.
func (s *Service) schemaToHold(ctx context.Context, actor application.Actor, named, schema string) (application.HeldSchema, error) {
	conn, err := s.chooseConnection(ctx, actor, named, schema)
	if err != nil {
		return application.HeldSchema{}, err
	}
	if other, err := s.Store.Holding(ctx, conn, schema); err == nil {
		return application.HeldSchema{}, fmt.Errorf("schema %s belongs to application %q: %w",
			application.SchemaPartID(conn, schema), other.Name, application.ErrSchemaHeld)
	} else if !errors.Is(err, application.ErrNotFound) {
		return application.HeldSchema{}, err //nolint:wrapcheck // the store's errors are read by identity
	}
	if err := s.canSeal(); err != nil {
		return application.HeldSchema{}, err
	}
	adopted, err := s.adoptedVersion(ctx, conn, schema)
	if err != nil {
		return application.HeldSchema{}, err
	}
	return application.HeldSchema{Connection: conn, Schema: schema, AdoptedVersion: adopted, AddedBy: actor.Email}, nil
}

// repairRunRole gives the schema an application already holds a run role
// when it has none.
func (s *Service) repairRunRole(ctx context.Context, a *application.Application) (*application.Application, error) {
	if _, err := s.Store.RunRoleOf(ctx, a.ID); err == nil {
		return a, nil
	} else if !errors.Is(err, application.ErrNoRunRole) {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	if err := s.canSeal(); err != nil {
		return nil, err
	}
	held := application.HeldSchema{Connection: a.Connection, Schema: a.Schema}
	if err := s.newRunRole(ctx, a.ID, &held); err != nil {
		return nil, err
	}
	if err := s.Store.SetRunRole(ctx, a.ID, held); err != nil {
		s.dropRunRole(ctx, a.Connection, a.Schema)
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// schemaTaken names the application holding a schema.
func (s *Service) schemaTaken(ctx context.Context, connection, schema string) error {
	if other, err := s.Store.Holding(ctx, connection, schema); err == nil {
		return fmt.Errorf("schema %s belongs to application %q: %w", application.SchemaPartID(connection, schema), other.Name, application.ErrSchemaHeld)
	}
	return application.ErrSchemaHeld
}

// canSeal refuses adding a schema when the run role's password could not be
// stored encrypted.
func (s *Service) canSeal() error {
	if s.Roles == nil {
		return refused("this deployment has no application database connection kind to hold a schema on")
	}
	if s.Sealer == nil || !s.Sealer.Enabled() {
		return refused("adding a schema creates a database role for the application's scripts, whose password the " +
			"platform stores encrypted; this deployment has no encryption key (ENCRYPTION_KEY), so no schema can be added")
	}
	return nil
}

// adoptedVersion is the migration version the schema has applied now, which
// the application adopts.
func (s *Service) adoptedVersion(ctx context.Context, connection, schema string) (int, error) {
	if s.Schema == nil {
		return 0, refused("this deployment keeps no migrations")
	}
	st, err := s.Schema.State(ctx, connection, schema)
	if err != nil {
		return 0, refused("schema %s cannot be added: %s", application.SchemaPartID(connection, schema), err.Error())
	}
	return st.Applied, nil
}

// newRunRole creates the run role of held's schema and seals its password
// onto held.
func (s *Service) newRunRole(ctx context.Context, applicationID string, held *application.HeldSchema) error {
	role, password, err := s.Roles.CreateRunRole(ctx, held.Connection, held.Schema)
	if err != nil {
		return &RefusedError{Msg: err.Error()}
	}
	sealed, err := s.Sealer.SealBound(RunRoleBinding(applicationID, held.Connection, held.Schema, role), password)
	if err != nil {
		s.dropRunRole(ctx, held.Connection, held.Schema)
		return fmt.Errorf("sealing the run role's password: %w", err)
	}
	held.Role, held.SealedPassword = role, sealed
	return nil
}

// dropRunRole drops a run role whose record was not written, logging a
// failure: the role is left for an administrator.
func (s *Service) dropRunRole(ctx context.Context, connection, schema string) {
	if err := s.Roles.DropRunRole(context.WithoutCancel(ctx), connection, schema); err != nil {
		slog.WarnContext(ctx, "application: dropping a run role whose record was not written failed",
			"connection", logsan.SanitizeForLog(connection), "schema", logsan.SanitizeForLog(schema),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// removeSchema takes the schema off the application and drops its run role.
func (s *Service) removeSchema(ctx context.Context, a *application.Application, id string) (*application.Application, error) {
	named, schema := application.ParseSchemaPartID(id)
	if a.Schema == "" || schema != a.Schema || (named != "" && named != a.Connection) {
		return nil, application.ErrNoPart
	}
	if s.Roles != nil {
		if err := s.Roles.DropRunRole(ctx, a.Connection, a.Schema); err != nil {
			return nil, fmt.Errorf("dropping the application's run role: %w", err)
		}
	}
	if err := s.Store.ClearSchema(ctx, a.ID); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// grantRunRole makes the run role a member of the write role a release's
// migrations defined, logging a failure: the next release tries again.
func (s *Service) grantRunRole(ctx context.Context, a *application.Application) {
	if s.Roles == nil || a.Schema == "" {
		return
	}
	if _, err := s.Roles.GrantRunRole(ctx, a.Connection, a.Schema); err != nil {
		slog.WarnContext(ctx, "application: granting the run role its schema's write role failed",
			"application", logsan.SanitizeForLog(a.Name), "error", logsan.SanitizeForLog(err.Error()))
	}
}

// SchemaOf is the schema part's view, nil for an application with none.
func (s *Service) SchemaOf(ctx context.Context, a *application.Application, current *application.Release) *SchemaView {
	if a.Schema == "" {
		return nil
	}
	v := &SchemaView{Connection: a.Connection, Schema: a.Schema, AdoptedVersion: a.SchemaAdoptedVersion, Pending: []int{}}
	if current != nil {
		v.ReleasedVersion = current.MigrationVersion
	}
	if s.Schema != nil {
		if st, err := s.Schema.State(ctx, a.Connection, a.Schema); err == nil {
			v.MigrationVersion, v.Pending = st.Applied, st.Pending
		} else {
			v.Problem = err.Error()
		}
	}
	if rr, err := s.Store.RunRoleOf(ctx, a.ID); err == nil {
		v.RunRole = rr.Role
	}
	s.describeRunRole(ctx, a, v)
	return v
}

// describeRunRole fills what the run role holds and still needs.
func (s *Service) describeRunRole(ctx context.Context, a *application.Application, v *SchemaView) {
	write := a.Schema + "_write"
	switch {
	case v.RunRole == "":
		v.Note = fmt.Sprintf("the application's scripts have no run role and run as the connection's own login; add the schema "+
			"again (add_part kind schema part_id %q) to create one", application.SchemaPartID(a.Connection, a.Schema))
	case s.Members != nil && s.Members(ctx, a.Connection, v.RunRole, write):
		v.WriteRole = write
	default:
		v.Note = fmt.Sprintf("the application's scripts log in as %s, which reaches no table until a migration defines %s "+
			"(CREATE ROLE %s NOLOGIN, with its grants on schema %s); the next release grants it to %s",
			v.RunRole, write, write, a.Schema, v.RunRole)
	}
}

// staysInside refuses adding a script whose source reads or writes a schema
// another application holds (#2539): an application's schema is read and
// written by its own scripts only. The source is read as the Flow diagram
// reads it, so a table it computes is not checked here; the database
// refuses that one when it runs.
func (s *Service) staysInside(ctx context.Context, a *application.Application, scriptID string) error {
	sc, err := s.script(ctx, scriptID)
	if err != nil || s.Scripts == nil {
		return err
	}
	v, err := s.Scripts.GetVersion(ctx, sc.ID, sc.Version)
	if err != nil || v == nil {
		return nil //nolint:nilerr // a script with no readable version has nothing to check
	}
	for _, ref := range scriptflow.DataRefs(v.Source) {
		if !s.onAppDB(ref) {
			continue
		}
		if err := s.tablesInside(ctx, a, scriptName(sc), ref); err != nil {
			return err
		}
	}
	return nil
}

// onAppDB reports whether a statement step names an application database
// connection, and names it literally.
func (s *Service) onAppDB(ref scriptflow.DataRef) bool {
	return !ref.Computed && ref.Connection != "" && (s.HasConnection == nil || s.HasConnection(ref.Connection))
}

// tablesInside refuses a statement step naming a table in a schema another
// application holds.
func (s *Service) tablesInside(ctx context.Context, a *application.Application, script string, ref scriptflow.DataRef) error {
	for _, table := range append(slices.Clone(ref.Reads), ref.Writes...) {
		schema := scriptflow.SchemaOf(table)
		other, err := s.Store.Holding(ctx, ref.Connection, schema)
		if err != nil || other.ID == a.ID {
			continue
		}
		return refused("script %q uses %s, in schema %s on connection %s, which belongs to application %q; only that "+
			"application's own scripts read and write it. Reach its data through what %q exposes, its endpoints and "+
			"exports, and then add the script", script, table, schema, ref.Connection, other.Name, other.Name)
	}
	return nil
}
