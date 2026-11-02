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
// (postgreskit.OwnDatabaseGuard). The platform runs it before any tool is
// registered, so no statement runs on a connection the check has not seen. A
// deployment with no database has nothing to protect and installs nothing.
func PostgresGuard(toolkits []registry.Toolkit, platformDB *sql.DB) {
	if platformDB == nil {
		return
	}
	guard := postgreskit.OwnDatabaseGuard(platformDB)
	for _, tk := range toolkits {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			pg.SetConnectGuard(guard)
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
	runner := func() scriptsession.AppDBRunner {
		for _, tk := range all() {
			if pg, ok := tk.(*postgreskit.Toolkit); ok {
				return pg
			}
		}
		return nil
	}
	checker, _ := authz.(connectionAuthorizer)
	scriptsession.InstallAppDB(scriptsession.AppDB{
		Runner:    runner,
		Authorize: func(ctx context.Context, connection string) error { return authorize(ctx, authn, checker, connection) },
	})
}

// errNoIdentity refuses a statement from a session that carries no identity.
var errNoIdentity = errors.New("the run carries no identity to authorize an application database with")

// authorize resolves the session's roles and asks the persona authorizer.
func authorize(ctx context.Context, authn middleware.Authenticator, checker connectionAuthorizer, connection string) error {
	if checker == nil {
		return nil
	}
	var roles []string
	if u := middleware.GetPreAuthenticatedUser(ctx); u != nil {
		roles = u.Roles
	} else if authn != nil {
		u, err := authn.Authenticate(ctx)
		if err != nil || u == nil {
			return errNoIdentity
		}
		roles = u.Roles
	} else {
		return errNoIdentity
	}
	if ok, _, reason := checker.IsConnectionAuthorized(ctx, roles, connection); !ok {
		return fmt.Errorf("application database %q is not allowed for this run: %s", connection, reason)
	}
	return nil
}
