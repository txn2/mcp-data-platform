// Package toolkitwire hands the platform's own dependencies to the toolkits
// that take them, by what each toolkit implements rather than by its kind: the
// metrics recorder to every toolkit that records its own (S3, the api gateway,
// postgres), and the platform's database to the postgres toolkits, whose every
// connection checks it did not reach that database (#2400).
//
// It is a seam of pkg/platform, extracted when that package reached its size
// budget; the platform calls it with its registry's toolkits.
package toolkitwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrefs"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// metricsAware is implemented by toolkits that record their own metrics.
type metricsAware interface {
	SetMetrics(*observability.Metrics)
}

// Metrics pushes m into every toolkit that records its own metrics. Each
// toolkit's SetMetrics decides what it installs for a disabled recorder, and
// is idempotent against the same recorder.
func Metrics(toolkits []registry.Toolkit, m *observability.Metrics) {
	for _, tk := range toolkits {
		if ma, ok := tk.(metricsAware); ok {
			ma.SetMetrics(m)
		}
	}
}

// PostgresGuard installs, on every postgres toolkit, the check each new
// database connection passes: that it did not reach the platform's database
// (postgreskit.OwnDatabaseGuard), the store manage_schema keeps its
// migrations in (#2404), and the saved scripts a migration's rename is
// checked against (#2405). The platform runs it before any tool is registered,
// so no statement runs on a connection the check has not seen and the toolkit
// lists manage_schema when it registers. A deployment with no database has
// nothing to protect and no store, and installs nothing.
func PostgresGuard(toolkits []registry.Toolkit, platformDB *sql.DB) {
	if platformDB == nil {
		return
	}
	guard := postgreskit.OwnDatabaseGuard(platformDB)
	store := migrationstore.New(platformDB)
	refs := scriptrefs.Lookup(platformDB)
	for _, tk := range toolkits {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			pg.SetConnectGuard(guard)
			pg.SetMigrationStore(store)
			pg.SetScriptReferences(refs)
			pg.StartTestSweep(platformDB)
		}
	}
}

// connectionAuthorizer is the persona authorizer's connection check
// (persona.Authorizer.IsConnectionAuthorized).
type connectionAuthorizer interface {
	IsConnectionAuthorized(ctx context.Context, roles []string, connection string) (allowed bool, personaName, reason string)
}

// AppDB installs what script sessions run application-database statements
// with (#2428): the live postgres toolkit, read from all on every lookup so a
// toolkit the registry gains later is the one used, and the check that the
// run's persona may use the connection. With an authorizer that has no
// connection check (no personas configured) every connection is allowed, as
// every connection is to that deployment's tool calls.
func AppDB(all func() []registry.Toolkit, authn middleware.Authenticator, authz middleware.Authorizer) {
	live := func() *postgreskit.Toolkit {
		for _, tk := range all() {
			if pg, ok := tk.(*postgreskit.Toolkit); ok {
				return pg
			}
		}
		return nil
	}
	runner := func() scriptsession.AppDBRunner {
		if pg := live(); pg != nil {
			return appdbRunner{pg}
		}
		return nil
	}
	checker, _ := authz.(connectionAuthorizer)
	scriptsession.InstallAppDB(scriptsession.AppDB{
		Runner: runner,
		Authorize: func(ctx context.Context, connection string) (string, error) {
			return authorize(ctx, authn, checker, connection)
		},
		TestDatabase: func(ctx context.Context, connection string, schemas []string) (scriptsession.AppDBRunner, func(), error) {
			pg := live()
			// A test's statements run as the test server's user: the run
			// must be allowed that connection as well as its own.
			if pg != nil {
				if srv := pg.TestServerOf(connection); srv != connection {
					if _, err := authorize(ctx, authn, checker, srv); err != nil {
						return nil, nil, err
					}
				}
			}
			return testDatabase(ctx, pg, connection, schemas)
		},
	})
}

// errNoAppDBToolkit refuses a test database on a deployment with no
// application database.
var errNoAppDBToolkit = errors.New("no application database is served on this deployment")

// testDatabase builds a script test's clone of connection (#2407).
func testDatabase(ctx context.Context, pg *postgreskit.Toolkit, connection string, schemas []string) (scriptsession.AppDBRunner, func(), error) {
	if pg == nil {
		return nil, nil, errNoAppDBToolkit
	}
	db, err := pg.TestDatabase(ctx, connection, schemas)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the toolkit's refusal names the connection and the fix
	}
	return appdbRunner{db.Toolkit}, db.Close, nil
}

// appdbRunner is the postgres toolkit as a script session runs statements on
// it: its transaction is handed back as the session's interface.
type appdbRunner struct{ *postgreskit.Toolkit }

// Begin implements scriptsession.AppDBRunner.
func (r appdbRunner) Begin(ctx context.Context, o postgreskit.TxOptions) (scriptsession.AppDBTx, error) {
	tx, err := r.Toolkit.Begin(ctx, o)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's typed failure, read by the session
	}
	return tx, nil
}

// errNoIdentity refuses a statement from a session that carries no identity.
var errNoIdentity = errors.New("the run carries no identity to authorize an application database with")

// authorize resolves the session's roles and asks the persona authorizer,
// returning the persona the connection was allowed as: the one whose login
// role the run's statements use (#2403). With no authorizer every connection
// is allowed, as no persona.
func authorize(ctx context.Context, authn middleware.Authenticator, checker connectionAuthorizer, connection string) (string, error) {
	if checker == nil {
		return "", nil
	}
	var roles []string
	if u := middleware.GetPreAuthenticatedUser(ctx); u != nil {
		roles = u.Roles
	} else if authn != nil {
		u, err := authn.Authenticate(ctx)
		if err != nil || u == nil {
			return "", errNoIdentity
		}
		roles = u.Roles
	} else {
		return "", errNoIdentity
	}
	ok, persona, reason := checker.IsConnectionAuthorized(ctx, roles, connection)
	if !ok {
		return "", fmt.Errorf("application database %q is not allowed for this run: %s", connection, reason)
	}
	return persona, nil
}
