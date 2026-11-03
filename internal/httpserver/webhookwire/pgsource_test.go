package webhookwire

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whpg"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// A postgres source reads the live postgres toolkit, found in the registry on
// every call, and none on a deployment that serves no application database.
func TestAppDBOf(t *testing.T) {
	assert.Nil(t, toolkitsOf(nil))
	none := appdbOf(func() []registry.Toolkit { return nil })
	assert.Nil(t, none())

	pg := postgreskit.NewMulti(postgreskit.MultiConfig{})
	t.Cleanup(func() { _ = pg.Close() })
	db := appdbOf(func() []registry.Toolkit { return []registry.Toolkit{pg} })()
	require.NotNil(t, db)
	_, err := db.Listen(context.Background(), "orders", []string{"order_events"})
	require.ErrorContains(t, err, `no application database named "orders"`)

	assert.NotNil(t, toolkitsOf(registry.NewRegistry()))
}

// The postgres listener is assembled beside the imap poller where the
// compactor runs, and writes through a writer of its own.
func TestAssemble_PostgresListener(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cfg := &platform.Config{}
	cfg.Webhooks.Compactor.Poll = time.Hour
	w, err := assemble(parts{
		db: dbOnly{db}, tables: whtable.New(&fakeExec{}, "b"), cfg: cfg, replica: "host:8080",
		appdb: func() whpg.Database { return nil },
	})
	require.NoError(t, err)
	assert.NotNil(t, w.pg)
	assert.NotNil(t, w.pgWriter)
	w.AttachEventRuns(nil)
	assert.NotNil(t, pgLock(db))
}

// A postgres source is refused at save when its database cannot be read: no
// application database, an unknown connection for its channels or its outbox.
func TestSourceDatabase_Check(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	assert.Nil(t, adminDatabase(nil, db))

	none := sourceDatabase{appdb: func() whpg.Database { return nil }}
	notify := whsource.Source{Config: whsource.Config{Input: whsource.InputPostgres, Postgres: &whsource.Postgres{
		Connection: "orders", Mode: whsource.PostgresNotify, Channels: []string{"order_events"},
	}}}
	require.ErrorIs(t, none.Check(context.Background(), notify), errNoAppDB)

	pg := postgreskit.NewMulti(postgreskit.MultiConfig{})
	t.Cleanup(func() { _ = pg.Close() })
	live, ok := adminDatabase(appdbOf(func() []registry.Toolkit { return []registry.Toolkit{pg} }), db).(sourceDatabase)
	require.True(t, ok)
	require.ErrorContains(t, live.Check(context.Background(), notify), `no application database named "orders"`)
	outbox := notify
	outbox.Config.Postgres = &whsource.Postgres{Connection: "orders", Mode: whsource.PostgresOutbox, Table: "public.outbox"}
	require.ErrorContains(t, live.Check(context.Background(), outbox), `no application database named "orders"`)
}
