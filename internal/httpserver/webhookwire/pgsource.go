package webhookwire

import (
	"context"
	"database/sql"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whpg"
	"github.com/txn2/mcp-data-platform/internal/webhook/whpgstate"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// appdbOf is the live postgres toolkit as a postgres source's database (#2408),
// read from the registry on every call so a toolkit the registry gains later
// is the one listened through; nil when the deployment serves none.
func appdbOf(all func() []registry.Toolkit) func() whpg.Database {
	return func() whpg.Database {
		for _, tk := range all() {
			if pg, ok := tk.(*postgreskit.Toolkit); ok {
				return pgDatabase{pg}
			}
		}
		return nil
	}
}

// pgDatabase is the postgres toolkit with its listener handed back as the
// manager's interface.
type pgDatabase struct{ *postgreskit.Toolkit }

// Listen implements whpg.Database.
func (d pgDatabase) Listen(ctx context.Context, connection string, channels []string) (whpg.Listener, error) {
	l, err := d.Toolkit.Listen(ctx, connection, channels)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit names the connection and channel
	}
	return l, nil
}

// pgLock holds a postgres source's listener to one replica at a time.
func pgLock(db *sql.DB) whpg.Lock {
	return func(ctx context.Context, source string) (func(), bool, error) {
		//nolint:wrapcheck // the lock package names itself
		return advisorylock.Try(ctx, db, advisorylock.Pair(whpg.LockKeyspace, whpg.LockKey(source)))
	}
}

// toolkitsOf is reg's toolkits as appdbOf reads them; nil without a registry.
func toolkitsOf(reg *registry.Registry) func() whpg.Database {
	if reg == nil {
		return nil
	}
	return appdbOf(reg.All)
}

// sourceDatabase is the postgres input's admin side: the check a source's
// database passes when it is saved, and its listener's state.
type sourceDatabase struct {
	appdb  func() whpg.Database
	states *whpgstate.States
}

// errNoAppDB refuses a postgres source on a deployment serving no
// application database.
var errNoAppDB = errors.New("no application database is served on this deployment")

// Check implements whadmin.Database: an outbox the connection's role can read
// and delete from, or channels it can listen on.
func (d sourceDatabase) Check(ctx context.Context, src whsource.Source) error {
	db, _ := d.appdb().(pgDatabase)
	if db.Toolkit == nil {
		return errNoAppDB
	}
	p := src.Config.Postgres
	if p.Mode == whsource.PostgresOutbox {
		return db.CheckOutbox(ctx, p.Connection, p.Table) //nolint:wrapcheck // names the connection and the table
	}
	l, err := db.Toolkit.Listen(ctx, p.Connection, p.Channels)
	if err != nil {
		return err //nolint:wrapcheck // names the connection and the channel
	}
	l.Close()
	return nil
}

// Listener implements whadmin.Database.
func (d sourceDatabase) Listener(ctx context.Context, source string) (whpgstate.State, error) {
	return d.states.Get(ctx, source) //nolint:wrapcheck // the store names itself
}

// adminDatabase is the admin side of the postgres input, nil when the
// deployment builds no listener.
func adminDatabase(appdb func() whpg.Database, db *sql.DB) whadmin.Database {
	if appdb == nil {
		return nil
	}
	return sourceDatabase{appdb: appdb, states: whpgstate.New(db)}
}
