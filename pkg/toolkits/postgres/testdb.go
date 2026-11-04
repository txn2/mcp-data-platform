package postgres

// Throwaway databases built from a connection's migrations (#2407): a
// template per migration head, built once by applying every migration from
// zero, and a clone of it per script test, made with CREATE DATABASE ...
// TEMPLATE so a test starts in well under a second. Building the template is
// also the check a migration passes before it saves: that it applies from
// zero on a database that never held anything else (#2405).
//
// Both live on the connection's test server: the server of the connection its
// test_connection names, or its own. Nothing here touches the connection's
// own database. Every database this file creates has a name of one of three
// forms, and nothing else is ever dropped:
//
//	mcpt_<connection hash>_<head hash>  a template, kept while it is the head
//	mcpb_<unix seconds>_<random>        a template being built
//	mcpc_<unix seconds>_<random>        a test's clone
//
// A clone is dropped when its test ends; a build or clone a dead run left
// behind is dropped by SweepTestDatabases once it is older than any run.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// The prefixes of the databases this file creates.
const (
	templatePrefix = "mcpt_"
	buildPrefix    = "mcpb_"
	clonePrefix    = "mcpc_"
)

// Codes a throwaway database is refused with.
const (
	// CodeCreateDBRequired is a test server whose user lacks CREATEDB.
	CodeCreateDBRequired = "createdb_required"
	// CodeFromZeroFailed is a migration set that does not apply from zero.
	CodeFromZeroFailed = "migration_from_zero_failed"
)

// templateActor is who the ledger of a template says applied its migrations.
const templateActor = "platform:test-database"

// scratchConns is the pool a throwaway database is reached with: a test's
// statements and one open transaction.
const scratchConns = 3

// dropTimeout bounds dropping a clone after its test, which runs on a context
// of its own so a canceled test still drops its clone.
const dropTimeout = 30 * time.Second

// cloneRetries is how many times a clone is retried while PostgreSQL reports
// its template in use (a template being renamed into place).
const cloneRetries = 10

// The hex lengths of a template's hashes and a scratch name's random part.
const (
	headLen     = 16
	familyLen   = 8
	randomBytes = 8
)

// objectInUse is SQLSTATE 55006: the template has another session.
const objectInUse = "55006"

// insufficientPrivilege is SQLSTATE 42501.
const insufficientPrivilege = "42501"

// scratchName is the name of a build or clone, with its creation time.
var scratchName = regexp.MustCompile(`^mcp[bc]_(\d+)_[0-9a-f]{16}$`)

// TestDB is a clone a script test runs against: a toolkit serving one
// connection, under the original connection's name, on the clone.
type TestDB struct {
	*Toolkit
	// Database is the clone's name.
	Database string
	drop     func()
}

// Close rolls back what the test left open, closes the pool and drops the
// clone.
func (d *TestDB) Close() {
	if d != nil && d.drop != nil {
		d.drop()
	}
}

// TestDatabase clones the template of connection's migrations in schemas
// (every schema they name when empty), building it first when the head has
// none, and returns a toolkit serving the clone under the connection's name.
func (t *Toolkit) TestDatabase(ctx context.Context, connection string, schemas []string) (*TestDB, error) {
	c, srv, err := t.testPair(connection)
	if err != nil {
		return nil, err
	}
	migrations, err := t.migrations.List(ctx, connection, "")
	if err != nil {
		return nil, fmt.Errorf(errReadMigrations, err)
	}
	if len(schemas) > 0 {
		migrations = slices.DeleteFunc(migrations, func(m Migration) bool { return !slices.Contains(schemas, m.Schema) })
	}
	return t.clone(ctx, c, srv, schemas, migrations)
}

// DescribeBuilt describes schema as migrations build it from zero, on a clone
// of their template dropped when it returns (#2406): what the live schema
// holds when nothing was changed outside migrations.
func (t *Toolkit) DescribeBuilt(ctx context.Context, connection, schema string, migrations []Migration) (*Description, error) {
	c, srv, err := t.testPair(connection)
	if err != nil {
		return nil, err
	}
	db, err := t.clone(ctx, c, srv, []string{schema}, migrations)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Describe(ctx, connection, schema)
}

// clone builds the template of migrations when the head has none and clones
// it on srv.
func (t *Toolkit) clone(ctx context.Context, c, srv *conn, schemas []string, migrations []Migration) (*TestDB, error) {
	connection := c.cfg.ConnectionName
	tmpl, err := t.template(ctx, c, srv, scopeOf(schemas), migrations)
	if err != nil {
		return nil, err
	}
	name, err := scratchDBName(clonePrefix, time.Now())
	if err != nil {
		return nil, err
	}
	if err := srv.cloneFrom(ctx, c, name, tmpl); err != nil {
		return nil, err
	}
	child, err := t.newScratch(connection, onServer(c.cfg, srv.cfg, name))
	if err != nil {
		srv.dropDatabase(name)
		return nil, err
	}
	return &TestDB{Toolkit: child, Database: name, drop: func() {
		_ = child.Close()
		srv.dropDatabase(name)
	}}, nil
}

// CheckFromZero builds the template of one schema's migrations, every one the
// schema would hold, which applies each of them from zero on a new database.
// The template is kept: once the migrations save it is the head a test of
// that schema clones.
func (t *Toolkit) CheckFromZero(ctx context.Context, connection, schema string, migrations []Migration) error {
	c, srv, err := t.testPair(connection)
	if err != nil {
		return err
	}
	_, err = t.template(ctx, c, srv, scopeOf([]string{schema}), migrations)
	return err
}

// scopeOf names the schemas a template holds: "" for every schema, or the
// sorted list.
func scopeOf(schemas []string) string {
	sorted := slices.Clone(schemas)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// TestServerOf is the connection whose server and credentials connection's
// throwaway databases are made with: its test_connection, or itself. A caller
// running statements there runs them as that connection's user, so the
// platform authorizes it as well.
func (t *Toolkit) TestServerOf(connection string) string {
	if c, ok := t.lookup(connection); ok && c.cfg.TestConnection != "" {
		return c.cfg.TestConnection
	}
	return connection
}

// testPair resolves a connection and its test server.
func (t *Toolkit) testPair(connection string) (c, srv *conn, err error) {
	if t.migrations == nil {
		return nil, nil, ErrNoMigrationStore
	}
	c, ok := t.lookup(connection)
	if !ok {
		return nil, nil, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: no application database named %q", errPrefix, connection)}
	}
	if c.cfg.TestConnection == "" || c.cfg.TestConnection == connection {
		return c, c, nil
	}
	srv, ok = t.lookup(c.cfg.TestConnection)
	if !ok {
		return nil, nil, &RefusedError{Code: CodeUnknownConnection, msg: fmt.Sprintf(
			"%s: connection %q names test_connection %q, which is not an application database on this deployment",
			errPrefix, connection, c.cfg.TestConnection)}
	}
	return c, srv, nil
}

// HeadOf is the hash naming a migration set's template: each migration's
// schema, version and checksum.
func HeadOf(migrations []Migration) string {
	h := sha256.New()
	for _, m := range migrations {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%s\n", m.Schema, m.Version, m.Checksum)
	}
	return hex.EncodeToString(h.Sum(nil))[:headLen]
}

// templateName is the template of a connection's migration set in scope.
func templateName(connection, scope string, migrations []Migration) string {
	return templateFamily(connection, scope) + HeadOf(migrations)
}

// templateFamily is the prefix every template of a connection's scope
// shares, so a new head replaces the scope's older template and no other.
func templateFamily(connection, scope string) string {
	sum := sha256.Sum256([]byte(connection + "\x00" + scope))
	return templatePrefix + hex.EncodeToString(sum[:])[:familyLen] + "_"
}

// scratchDBName is a new build or clone name.
func scratchDBName(prefix string, now time.Time) (string, error) {
	var b [randomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("appdb: naming a test database: %w", err)
	}
	return prefix + strconv.FormatInt(now.Unix(), decimal) + "_" + hex.EncodeToString(b[:]), nil
}

// onServer is a connection's configuration pointed at database on the test
// server: the test server's address and credentials, the connection's own
// limits. A connection's persona roles (#2403) are kept when the test server
// is the connection's own server, where the roles exist, so a script test
// runs each statement as the role it would run as against the connection;
// on another server they are dropped and every statement runs as the test
// server's user.
func onServer(c, srv Config, database string) Config {
	out := c
	if c.Host != srv.Host || c.Port != srv.Port {
		out.PersonaRoles, out.PersonaRolePasswords = nil, nil
	}
	out.Host, out.Port, out.User, out.Password = srv.Host, srv.Port, srv.User, srv.Password
	out.SSLMode, out.CABundlePEM, out.ClientCertPEM, out.ClientKeyPEM = srv.SSLMode, srv.CABundlePEM, srv.ClientCertPEM, srv.ClientKeyPEM
	out.Database, out.MaxConns, out.TestConnection = database, scratchConns, ""
	return out
}

// newScratch is scratch, or the stand-in a unit test installs.
func (t *Toolkit) newScratch(name string, cfg Config) (*Toolkit, error) {
	if t.scratchHook != nil {
		return t.scratchHook(name, cfg)
	}
	return t.scratch(name, cfg)
}

// scratch is a toolkit serving one connection on a throwaway database, with
// the guard every connection passes.
func (t *Toolkit) scratch(name string, cfg Config) (*Toolkit, error) {
	child := &Toolkit{name: name, defaultName: name, connections: map[string]*conn{}, dbCfg: t.dbCfg}
	if g := t.guard.Load(); g != nil {
		child.guard.Store(g)
	}
	c, err := child.newConn(name, cfg)
	if err != nil {
		return nil, err
	}
	child.connections[name] = c
	return child, nil
}

// template returns the template of migrations on the test server, building it
// when there is none. Building holds an advisory lock per connection on the
// test server, so two replicas build it once.
func (t *Toolkit) template(ctx context.Context, c, srv *conn, scope string, migrations []Migration) (string, error) {
	s, err := srv.session(ctx)
	if err != nil {
		return "", srv.failure(ctx, err)
	}
	defer s.Release()
	if err := mayCreate(ctx, s, c, srv); err != nil {
		return "", err
	}
	name := templateName(c.cfg.ConnectionName, scope, migrations)
	if _, err := s.Exec(ctx, "SELECT pg_advisory_lock(hashtext('mcp_test_template'), hashtext($1))", c.cfg.ConnectionName); err != nil {
		return "", srv.failure(ctx, err)
	}
	var exists bool
	if err := s.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", srv.failure(ctx, err)
	}
	if exists {
		return name, nil
	}
	build, err := scratchDBName(buildPrefix, time.Now())
	if err != nil {
		return "", err
	}
	if err := s.Script(ctx, "CREATE DATABASE "+build); err != nil {
		return "", srv.failure(ctx, err)
	}
	if err := t.applyFromZero(ctx, c, srv, build, migrations); err != nil {
		srv.dropDatabase(build)
		return "", err
	}
	if err := s.Script(ctx, "ALTER DATABASE "+build+" RENAME TO "+name); err != nil {
		srv.dropDatabase(build)
		return "", srv.failure(ctx, err)
	}
	dropOlderTemplates(ctx, s, templateFamily(c.cfg.ConnectionName, scope), name)
	return name, nil
}

// mayCreate refuses a test server whose user cannot create databases, naming
// the privilege.
func mayCreate(ctx context.Context, s session, c, srv *conn) error {
	var ok bool
	if err := s.QueryRow(ctx, "SELECT rolcreatedb OR rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user").Scan(&ok); err != nil {
		return srv.failure(ctx, err)
	}
	if !ok {
		return createDBRefusal(c, srv)
	}
	return nil
}

// createDBRefusal names the missing privilege and the two ways to supply it.
func createDBRefusal(c, srv *conn) error {
	return &RefusedError{Code: CodeCreateDBRequired, msg: fmt.Sprintf(
		"%s: user %q of connection %q lacks the CREATEDB privilege, which the throwaway databases of connection %q's "+
			"migration checks and script tests are created with; grant it (ALTER ROLE %s CREATEDB), or set the connection's "+
			"test_connection to an application database whose user has it",
		errPrefix, srv.cfg.User, srv.cfg.ConnectionName, c.cfg.ConnectionName, srv.cfg.User)}
}

// applyFromZero applies every migration, schema by schema in version order,
// to the new database build.
func (t *Toolkit) applyFromZero(ctx context.Context, c, srv *conn, build string, migrations []Migration) error {
	cfg := onServer(c.cfg, srv.cfg, build)
	cfg.ReadOnly = false
	child, err := t.newScratch(c.cfg.ConnectionName, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = child.Close() }()
	for start := 0; start < len(migrations); {
		end := start
		for end < len(migrations) && migrations[end].Schema == migrations[start].Schema {
			end++
		}
		o := ApplyOptions{Connection: c.cfg.ConnectionName, Schema: migrations[start].Schema, Actor: templateActor}
		if _, err := child.Apply(ctx, o, migrations[start:end]); err != nil {
			return &RefusedError{Code: CodeFromZeroFailed, msg: fmt.Sprintf(
				"%s: the migrations of connection %q do not apply from zero on a new database: schema %s: %v",
				errPrefix, c.cfg.ConnectionName, migrations[start].Schema, err)}
		}
		start = end
	}
	return nil
}

// cloneFrom creates database name from template tmpl, waiting out the moment
// a template is renamed into place.
func (srv *conn) cloneFrom(ctx context.Context, c *conn, name, tmpl string) error {
	s, err := srv.session(ctx)
	if err != nil {
		return srv.failure(ctx, err)
	}
	defer s.Release()
	for attempt := 1; ; attempt++ {
		err = s.Script(ctx, "CREATE DATABASE "+name+" TEMPLATE "+tmpl)
		if err == nil {
			return nil
		}
		if sqlState(err) != objectInUse || attempt >= cloneRetries {
			break
		}
		select {
		case <-ctx.Done():
			return srv.failure(ctx, ctx.Err())
		case <-time.After(time.Duration(attempt) * 20 * time.Millisecond):
		}
	}
	if sqlState(err) == insufficientPrivilege {
		return createDBRefusal(c, srv)
	}
	return srv.failure(ctx, err)
}

// dropOlderTemplates drops the other templates of a family. One a clone is
// being made from is in use and is left for the next build.
func dropOlderTemplates(ctx context.Context, s session, family, keep string) {
	rows, err := s.Query(ctx, "SELECT datname FROM pg_catalog.pg_database WHERE starts_with(datname, $1) AND datname <> $2",
		family, keep)
	if err != nil {
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	rows.Close()
	for _, n := range names {
		if err := s.Script(ctx, "DROP DATABASE IF EXISTS "+n); err != nil {
			slog.DebugContext(ctx, "appdb: an older test template is in use; leaving it", "database", n)
		}
	}
}

// dropDatabase drops a build or clone, forcing off any session still on it.
// It runs on a context of its own: the test that made the clone may be the
// thing that was canceled. A failure is logged; the sweep drops it later.
func (srv *conn) dropDatabase(name string) {
	if !scratchName.MatchString(name) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()
	s, err := srv.session(ctx)
	if err == nil {
		err = s.Script(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		s.Release()
	}
	if err != nil {
		slog.WarnContext(ctx, "appdb: dropping a test database failed; the sweep drops it", "database", name,
			"connection", logsan.SanitizeForLog(srv.cfg.ConnectionName), "error", logsan.SanitizeForLog(scrubConnString(err.Error())))
	}
}

// SweepTestDatabases drops every build and clone older than olderThan on each
// connection's test server that the server's user owns: the ones a run that
// died never dropped. It returns how many it dropped.
func (t *Toolkit) SweepTestDatabases(ctx context.Context, olderThan time.Duration) (int, error) {
	servers := t.testServers()
	cutoff := time.Now().Add(-olderThan)
	dropped := 0
	var errs []error
	for _, srv := range servers {
		n, err := srv.sweep(ctx, cutoff)
		dropped += n
		if err != nil {
			errs = append(errs, fmt.Errorf("connection %s: %w", srv.cfg.ConnectionName, err))
		}
	}
	return dropped, errors.Join(errs...) //nolint:wrapcheck // each joined error names its connection
}

// testServers is each distinct test server among the connections.
func (t *Toolkit) testServers() []*conn {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := map[*conn]bool{}
	var out []*conn
	for name, c := range t.connections {
		srv := c
		if c.cfg.TestConnection != "" && c.cfg.TestConnection != name {
			if other, ok := t.connections[c.cfg.TestConnection]; ok {
				srv = other
			}
		}
		if !seen[srv] {
			seen[srv] = true
			out = append(out, srv)
		}
	}
	return out
}

// sweep drops the builds and clones on one server created before cutoff.
func (srv *conn) sweep(ctx context.Context, cutoff time.Time) (int, error) {
	s, err := srv.session(ctx)
	if err != nil {
		return 0, srv.failure(ctx, err)
	}
	defer s.Release()
	rows, err := s.Query(ctx, `SELECT datname FROM pg_catalog.pg_database WHERE datname ~ '^mcp[bc]_[0-9]+_[0-9a-f]{16}$' AND pg_catalog.pg_get_userbyid(datdba) = current_user`)
	if err != nil {
		return 0, srv.failure(ctx, err)
	}
	var stale []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && createdBefore(n, cutoff) {
			stale = append(stale, n)
		}
	}
	rows.Close()
	dropped := 0
	for _, n := range stale {
		if err := s.Script(ctx, "DROP DATABASE IF EXISTS "+n+" WITH (FORCE)"); err != nil {
			return dropped, srv.failure(ctx, err)
		}
		dropped++
	}
	return dropped, nil
}

// createdBefore reports whether a build or clone name was created before
// cutoff.
func createdBefore(name string, cutoff time.Time) bool {
	m := scratchName.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	secs, err := strconv.ParseInt(m[1], decimal, float64Bits)
	return err == nil && time.Unix(secs, 0).Before(cutoff)
}
