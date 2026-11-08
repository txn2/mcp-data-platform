package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// applications is what the toolkit asks about the applications that hold an
// application database's schemas (#2539). The platform installs it; the
// toolkit keeps no record of applications.
//
// A schema exists only inside an application: manage_schema adds, plans,
// applies and reverts migrations only on a schema an application holds. An
// application's scripts log in as its run role, which the platform creates
// when the schema is added to the application and stores sealed; RunRole
// opens it when a pool is built for it, and nowhere else.
type applications struct {
	// holder is the name of the application holding schema on connection,
	// "" for none.
	holder func(ctx context.Context, connection, schema string) (string, error)
	// runRole is the login role, and its password, the statements of the
	// application's scripts run as on connection; role is "" when the
	// application holds no schema there.
	runRole func(ctx context.Context, connection, application string) (role, password string, err error)
}

// Holder implements the lookup of the application holding a schema.
func (a *applications) Holder(ctx context.Context, connection, schema string) (string, error) {
	return a.holder(ctx, connection, schema)
}

// RunRole implements the lookup of an application's run role.
func (a *applications) RunRole(ctx context.Context, connection, application string) (role, password string, err error) {
	return a.runRole(ctx, connection, application)
}

// SetApplications installs what the toolkit asks about applications: holder,
// the name of the application holding a schema ("" for none), and runRole,
// the login role and password an application's scripts run as on a
// connection (role "" for none). Either nil leaves every schema governed by
// no application, as before #2539.
func (t *Toolkit) SetApplications(
	holder func(ctx context.Context, connection, schema string) (string, error),
	runRole func(ctx context.Context, connection, application string) (role, password string, err error),
) {
	if holder == nil || runRole == nil {
		t.apps.Store(nil)
		return
	}
	t.apps.Store(&applications{holder: holder, runRole: runRole})
}

// applications is the installed lookups, nil for none.
func (t *Toolkit) applications() *applications { return t.apps.Load() }

// codeSchemaNotHeld refuses a migration command on a schema no application
// holds (#2539).
const codeSchemaNotHeld = "schema_not_held"

// held refuses a command that migrates schema when no application holds it.
func (t *Toolkit) held(ctx context.Context, connection, schema string) error {
	apps := t.applications()
	if apps == nil {
		return nil
	}
	holder, err := apps.Holder(ctx, connection, schema)
	if err != nil {
		return fmt.Errorf("appdb: reading which application holds schema %q: %w", schema, err)
	}
	if holder != "" {
		return nil
	}
	return &RefusedError{Code: codeSchemaNotHeld, msg: fmt.Sprintf(
		"%s: schema %q on connection %q belongs to no application, and a schema's migrations are added, planned and "+
			"applied only inside the application that holds it. Add it first: manage_application add_part with your "+
			"application, kind %q and part_id %q. Then add its migrations here and release the application.",
		errPrefix, schema, connection, "schema", connection+"/"+schema)}
}

// unheldSchema is a schema of a connection whose migrations no application
// governs: one with a migration in the ledger or kept by the platform that no
// application holds (#2539). Migrations on it are refused until AddPart adds
// it to an application.
type unheldSchema struct {
	Schema string `json:"schema" example:"shop"`
	// AddPart is the one call that adds it to an application.
	AddPart string `json:"add_part" example:"manage_application add_part application=<name> kind=schema part_id=orders/shop"`
}

// unheldSchemas lists connection's schemas with migrations that no
// application holds, sorted; empty when no Applications is installed.
func (t *Toolkit) unheldSchemas(ctx context.Context, connection string) ([]unheldSchema, error) {
	out := []unheldSchema{}
	apps := t.applications()
	if apps == nil {
		return out, nil
	}
	names, err := t.migratedSchemas(ctx, connection)
	if err != nil {
		return nil, err
	}
	for _, s := range names {
		holder, err := apps.Holder(ctx, connection, s)
		if err != nil {
			return nil, fmt.Errorf("appdb: reading which application holds schema %q: %w", s, err)
		}
		if holder == "" {
			out = append(out, unheldSchema{Schema: s, AddPart: addPartCall(connection, s)})
		}
	}
	return out, nil
}

// addPartCall is the call that adds a schema to an application.
func addPartCall(connection, schema string) string {
	return "manage_application add_part application=<name> kind=schema part_id=" + connection + "/" + schema
}

// migratedSchemas is every schema of connection with a migration: applied,
// in the target's ledger, or kept by the platform.
func (t *Toolkit) migratedSchemas(ctx context.Context, connection string) ([]string, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return nil, fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	seen := map[string]bool{}
	if t.migrations != nil {
		kept, err := t.migrations.List(ctx, connection, "")
		if err != nil {
			return nil, fmt.Errorf(errReadMigrations, err)
		}
		for _, m := range kept {
			seen[m.Schema] = true
		}
	}
	ledger, err := c.ledgerSchemas(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range ledger {
		seen[s] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// ledgerSchemasSQL lists the schemas the ledger records a migration of.
func (c *conn) ledgerSchemasSQL() string {
	return "/* mcp-data-platform */ SELECT DISTINCT schema_name FROM " + c.ledger() + " ORDER BY 1"
}

// ledgerSchemas lists the schemas the target's ledger records a migration
// of; none when there is no ledger yet.
func (c *conn) ledgerSchemas(ctx context.Context) ([]string, error) {
	q := c.ledgerQ
	if q == nil {
		q = c.pool
	}
	var exists bool
	if err := q.QueryRow(ctx, ledgerExistsSQL, c.ledger()).Scan(&exists); err != nil {
		return nil, fmt.Errorf("appdb: reading the ledger of connection %q: %s", c.cfg.ConnectionName, scrubDriverError(err))
	}
	if !exists {
		return []string{}, nil
	}
	rows, err := q.Query(ctx, c.ledgerSchemasSQL())
	if err != nil {
		return nil, fmt.Errorf("appdb: reading the ledger of connection %q: %s", c.cfg.ConnectionName, scrubDriverError(err))
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("appdb: reading the ledger: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err() //nolint:wrapcheck // read by its type
}

// runRoleNameFor is the login role an application holding schema in database
// runs its scripts' statements as: the schema's name, "_run", and eight hex
// digits of the database's name. A role belongs to the whole server, so two
// databases on one server holding a schema of the same name get two roles.
// The schema's part is shortened to keep the name within PostgreSQL's 63
// bytes.
func runRoleNameFor(database, schema string) string {
	sum := sha256.Sum256([]byte(database))
	suffix := "_run_" + hex.EncodeToString(sum[:])[:runRoleSuffixHex]
	if len(schema)+len(suffix) > maxIdentifierBytes {
		schema = schema[:maxIdentifierBytes-len(suffix)]
	}
	return schema + suffix
}

// runRoleSuffixHex is how many hex digits of the database name's digest a
// run role's name ends with.
const runRoleSuffixHex = 8

// maxIdentifierBytes is the longest identifier PostgreSQL keeps; a longer one
// is cut.
const maxIdentifierBytes = 63

// runRoleName is the run role of schema on c's database.
func (c *conn) runRoleName(schema string) string { return runRoleNameFor(c.cfg.Database, schema) }

// writeRoleName is the role an application's migrations define for writing
// its schema, which the run role is granted when it exists.
func writeRoleName(schema string) string { return schema + "_write" }

// codeCreateRoleRequired refuses adding a schema to an application on a
// connection whose user cannot create its run role (#2539).
const codeCreateRoleRequired = "createrole_required"

// codeRoleExists refuses a run role whose name another role holds.
const codeRoleExists = "role_exists"

// The statements the run role is managed with.
const (
	canCreateRoleSQL = "/* mcp-data-platform */ SELECT rolcreaterole OR rolsuper FROM pg_roles WHERE rolname = current_user"
	roleExistsSQL    = "/* mcp-data-platform */ SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)"
	memberOfSQL      = "/* mcp-data-platform */ SELECT pg_has_role($1, $2, 'MEMBER')"
)

// CreateRunRole creates the login role an application holding schema runs
// its scripts' statements as (#2539): LOGIN, and none of SUPERUSER,
// CREATEDB, CREATEROLE, REPLICATION or BYPASSRLS, with a password generated
// here. The role is sent its SCRAM-SHA-256 verifier, never the password, so
// no statement text the server keeps (pg_stat_statements, the server log)
// holds it. It is made a member of the schema's write role when the
// schema's migrations define one. The connection's user needs CREATEROLE. It
// answers the role and the password it logs in with, which the caller
// stores sealed and never shows.
func (t *Toolkit) CreateRunRole(ctx context.Context, connection, schema string) (role, password string, err error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return "", "", err
	}
	role = c.runRoleName(schema)
	if err := c.mayCreateRole(ctx, connection, schema, role); err != nil {
		return "", "", err
	}
	password, verifier, err := newRunRolePassword()
	if err != nil {
		return "", "", err
	}
	create := "/* mcp-data-platform */ CREATE ROLE " + pgx.Identifier{role}.Sanitize() +
		" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '" + verifier + "'"
	if _, err := c.roleQ().Exec(ctx, create); err != nil {
		return "", "", c.failure(ctx, err)
	}
	if _, err := t.GrantRunRole(ctx, connection, schema); err != nil {
		_ = t.DropRunRole(context.WithoutCancel(ctx), connection, schema)
		return "", "", err
	}
	return role, password, nil
}

// mayCreateRole refuses creating role when the connection's user cannot
// create roles or a role of that name is on the server.
func (c *conn) mayCreateRole(ctx context.Context, connection, schema, role string) error {
	var can bool
	if err := c.roleQ().QueryRow(ctx, canCreateRoleSQL).Scan(&can); err != nil {
		return c.failure(ctx, err)
	}
	if !can {
		return &RefusedError{Code: codeCreateRoleRequired, msg: fmt.Sprintf(
			"%s: the user of connection %q cannot create roles, and an application's statements run as a role the "+
				"platform creates when its schema is added. Whoever runs the database server grants it once: ALTER ROLE %s CREATEROLE",
			errPrefix, connection, pgx.Identifier{c.cfg.User}.Sanitize())}
	}
	var exists bool
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, role).Scan(&exists); err != nil {
		return c.failure(ctx, err)
	}
	if exists {
		return &RefusedError{Code: codeRoleExists, msg: fmt.Sprintf(
			"%s: a role named %s already exists on the server of connection %q, so schema %q cannot be given its run role. "+
				"Another database on the server may hold a schema of the same name; choose another schema name, or have "+
				"whoever runs the server drop the role", errPrefix, role, connection, schema)}
	}
	return nil
}

// GrantRunRole makes the run role of schema a member of the schema's write
// role when the schema's migrations define one and the run role is not yet,
// naming the role granted; "" when there was nothing to grant. A release
// calls it after applying migrations, since the migration that defines the
// write role usually comes after the schema was added.
func (t *Toolkit) GrantRunRole(ctx context.Context, connection, schema string) (string, error) {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return "", err
	}
	run, write := c.runRoleName(schema), writeRoleName(schema)
	var runExists, writeExists bool
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, run).Scan(&runExists); err != nil {
		return "", c.failure(ctx, err)
	}
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, write).Scan(&writeExists); err != nil {
		return "", c.failure(ctx, err)
	}
	if !runExists || !writeExists {
		return "", nil
	}
	var member bool
	if err := c.roleQ().QueryRow(ctx, memberOfSQL, run, write).Scan(&member); err != nil {
		return "", c.failure(ctx, err)
	}
	if member {
		return "", nil
	}
	grant := "/* mcp-data-platform */ GRANT " + pgx.Identifier{write}.Sanitize() + " TO " + pgx.Identifier{run}.Sanitize()
	if _, err := c.roleQ().Exec(ctx, grant); err != nil {
		return "", c.failure(ctx, err)
	}
	return write, nil
}

// DropRunRole drops the run role of schema on connection: the pools logged in
// as it on this process are closed, and what it owns and was granted in the
// database goes with it. A role already gone is not an error.
func (t *Toolkit) DropRunRole(ctx context.Context, connection, schema string) error {
	c, err := t.schemaConn(connection, schema)
	if err != nil {
		return err
	}
	role := c.runRoleName(schema)
	c.forgetRole(role)
	var exists bool
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, role).Scan(&exists); err != nil {
		return c.failure(ctx, err)
	}
	if !exists {
		return nil
	}
	ident := pgx.Identifier{role}.Sanitize()
	if _, err := c.roleQ().Exec(ctx, "/* mcp-data-platform */ DROP OWNED BY "+ident); err != nil {
		return c.failure(ctx, err)
	}
	if _, err := c.roleQ().Exec(ctx, "/* mcp-data-platform */ DROP ROLE IF EXISTS "+ident); err != nil {
		return c.failure(ctx, err)
	}
	return nil
}

// scramIterations is the PBKDF2 iteration count of a run role's verifier,
// PostgreSQL's own default.
const scramIterations = 4096

// newRunRolePassword generates a run role's password, 32 random bytes, and
// the SCRAM-SHA-256 verifier PostgreSQL stores for it (RFC 7677, in
// PostgreSQL's SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey>
// form). Holding the verifier does not let anyone log in.
func newRunRolePassword() (password, verifier string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("appdb: generating a password: %w", err)
	}
	password = base64.RawURLEncoding.EncodeToString(raw)
	verifier, err = scramVerifier(password)
	return password, verifier, err
}

// scramVerifier is password's SCRAM-SHA-256 verifier with a fresh salt.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("appdb: generating a salt: %w", err)
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, scramIterations, sha256.Size)
	if err != nil {
		return "", fmt.Errorf("appdb: deriving the verifier: %w", err)
	}
	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		_, _ = h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	server := mac(salted, "Server Key")
	enc := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", scramIterations, enc(salt), enc(stored[:]), enc(server)), nil
}

// appPoolKey is the key an application's run-role pool is kept under among a
// connection's role pools, apart from every persona's.
func appPoolKey(role string) string { return "\x00run:" + role }

// runRolePool is the pool the statements of application's scripts run on:
// its run role's, built from the password Applications opens the first time
// one runs. ok is false when the application holds no schema on c.
func (t *Toolkit) runRolePool(ctx context.Context, c *conn, application string) (*pgxpool.Pool, bool, error) {
	apps := t.applications()
	if apps == nil || application == "" {
		return nil, false, nil
	}
	role, password, err := apps.RunRole(ctx, c.cfg.ConnectionName, application)
	if err != nil {
		return nil, false, fmt.Errorf("appdb: reading the run role of application %q: %w", application, err)
	}
	if role == "" {
		return nil, false, nil
	}
	p, err := c.rolePool(appPoolKey(role), role, password)
	if err != nil {
		return nil, false, fmt.Errorf("appdb: building the pool of application %q's role on connection %q: %w", application, c.cfg.ConnectionName, err)
	}
	return p, true, nil
}

// forgetRole closes and forgets the pool logged in as role.
func (c *conn) forgetRole(role string) {
	if c.roles == nil {
		return
	}
	c.roles.mu.Lock()
	p, ok := c.roles.pools[appPoolKey(role)]
	delete(c.roles.pools, appPoolKey(role))
	c.roles.mu.Unlock()
	if ok {
		p.Close()
	}
}

// errNoRolePools is a role pool asked of a connection built without them.
var errNoRolePools = errors.New("the connection builds no role pools")

// rolePool is the pool kept under key that logs in as role with password,
// built the first time it is asked for.
func (c *conn) rolePool(key, role, password string) (*pgxpool.Pool, error) {
	if c.roles == nil {
		return nil, errNoRolePools
	}
	c.roles.mu.Lock()
	defer c.roles.mu.Unlock()
	if p, ok := c.roles.pools[key]; ok {
		return p, nil
	}
	rc := c.cfg
	rc.User, rc.Password = role, password
	p, err := c.roles.build(rc)
	if err != nil {
		return nil, err
	}
	c.roles.pools[key] = p
	return p, nil
}

// identityPool is the pool a statement for id runs on: the run role's of
// the application id names when it holds a schema on c, else the persona's
// role, else the connection's own.
func (c *conn) identityPool(ctx context.Context, id Identity) (*pgxpool.Pool, error) {
	if id.Application != "" && c.runPool != nil {
		p, ok, err := c.runPool(ctx, c, id.Application)
		if err != nil {
			return nil, err
		}
		if ok {
			return p, nil
		}
	}
	return c.poolFor(id.Persona)
}

// IsMember reports whether member is a member of role on connection; false
// when either does not exist.
func (t *Toolkit) IsMember(ctx context.Context, connection, member, role string) (bool, error) {
	c, ok := t.lookup(connection)
	if !ok {
		return false, fmt.Errorf("appdb: no application database named %q: %w", connection, ErrConnectionNotFound)
	}
	var memberExists, roleExists bool
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, member).Scan(&memberExists); err != nil {
		return false, c.failure(ctx, err)
	}
	if err := c.roleQ().QueryRow(ctx, roleExistsSQL, role).Scan(&roleExists); err != nil {
		return false, c.failure(ctx, err)
	}
	if !memberExists || !roleExists {
		return false, nil
	}
	var is bool
	if err := c.roleQ().QueryRow(ctx, memberOfSQL, member, role).Scan(&is); err != nil {
		return false, c.failure(ctx, err)
	}
	return is, nil
}

// roleQuerier is what the run-role statements are sent on: the connection's
// pool as its own user, or a test's stand-in.
type roleQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// roleQ is c's role querier.
func (c *conn) roleQ() roleQuerier {
	if c.roles != nil && c.roles.q != nil {
		return c.roles.q
	}
	return c.pool
}
