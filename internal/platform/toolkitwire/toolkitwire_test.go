package toolkitwire

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// recording is a toolkit that records its own metrics.
type recording struct {
	got *observability.Metrics
	set bool
}

func (*recording) Kind() string                          { return "fake" }
func (*recording) Name() string                          { return "fake" }
func (*recording) Connection() string                    { return "" }
func (*recording) RegisterTools(*mcp.Server)             {}
func (*recording) Tools() []string                       { return nil }
func (*recording) SetSemanticProvider(semantic.Provider) {}
func (*recording) SetQueryProvider(query.Provider)       {}
func (*recording) Close() error                          { return nil }
func (r *recording) SetMetrics(m *observability.Metrics) { r.got, r.set = m, true }

func TestMetricsReachesEveryToolkitThatRecords(t *testing.T) {
	r := &recording{}
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	Metrics([]registry.Toolkit{r, pg}, nil)
	if !r.set {
		t.Fatal("the recorder did not reach a toolkit that records its own metrics")
	}
}

func TestPostgresGuardNeedsAPlatformDatabase(t *testing.T) {
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	// No database: nothing to protect, nothing installed, and a save is not
	// asked to reach its database.
	PostgresGuard([]registry.Toolkit{pg}, nil)
	if err := pg.ValidateConnection("c", map[string]any{"host": "127.0.0.1", "port": 9, "database": "d", "user": "u", "sslmode": "disable"}); err != nil {
		t.Fatalf("validate without a guard: %v", err)
	}
	db, err := sql.Open("postgres", "postgres://u:p@127.0.0.1:9/d?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	assert.Empty(t, pg.Tools(), "no store, no manage_schema")
	PostgresGuard([]registry.Toolkit{&recording{}, pg}, db)
	assert.Equal(t, []string{postgreskit.SchemaToolName}, pg.Tools(), "the store is installed with the guard (#2404)")
}

// The session runner hands the toolkit's transaction back as the session's
// interface, and a refusal back as the toolkit's own (#2402).
func TestAppDBRunnerBegin(t *testing.T) {
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	_, err := appdbRunner{pg}.Begin(context.Background(), postgreskit.TxOptions{Connection: "nope"})
	var refused *postgreskit.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, postgreskit.CodeUnknownConnection, refused.Code)

	_, err = appdbRunner{pg}.OpenCursor(context.Background(), postgreskit.Statement{Connection: "nope", SQL: "SELECT 1"}, 10)
	require.ErrorAs(t, err, &refused, "a cursor on an unknown connection is refused as the toolkit refuses it")
	_, err = appdbTx{&postgreskit.Tx{}}.OpenCursor(context.Background(), postgreskit.Statement{}, 10)
	require.ErrorAs(t, err, &refused, "an empty statement is refused before the transaction is touched")
	k, err := cursorOf(&postgreskit.Cursor{}, nil)
	require.NoError(t, err)
	assert.NotNil(t, k)
}

// connChecker allows one connection and records the roles it was asked about.
type connChecker struct{ roles []string }

func (*connChecker) IsAuthorized(context.Context, string, []string, string, string) (allowed bool, persona, reason string) {
	return true, "", ""
}

func (c *connChecker) IsConnectionAuthorized(_ context.Context, roles []string, conn string) (allowed bool, persona, reason string) {
	c.roles = roles
	if conn == "shop" {
		return true, "builder", ""
	}
	return false, "builder", "connection " + conn + " not allowed for persona: builder"
}

// fixedAuthn authenticates every context as one user, or fails.
type fixedAuthn struct {
	user *middleware.UserInfo
	err  error
}

func (a fixedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) { return a.user, a.err }

func TestAppDB_InstallsTheLiveToolkitAndThePersonaCheck(t *testing.T) {
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	toolkits := make([]registry.Toolkit, 0, 2)
	toolkits = append(toolkits, &recording{})
	all := func() []registry.Toolkit { return toolkits }
	checker := &connChecker{}
	AppDB(all, fixedAuthn{err: errors.New("no token")}, checker)
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })

	assert.False(t, scriptsession.IsAppDB("shop"), "no postgres toolkit registered yet")
	require.NoError(t, pg.AddConnection("shop", map[string]any{"host": "localhost", "database": "shop", "user": "u", "sslmode": "disable"}))
	toolkits = append(toolkits, pg)
	assert.True(t, scriptsession.IsAppDB("shop"), "a toolkit the registry gains later is found")

	ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{Roles: []string{"dp_builder"}})
	err := scriptsession.NewTestDatabases(ctx).Open("shop", nil)
	assert.ErrorIs(t, err, postgreskit.ErrNoMigrationStore, "a test database is built by the live toolkit, which keeps no migrations here")
}

func TestAuthorize(t *testing.T) {
	checker := &connChecker{}
	ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{Roles: []string{"dp_builder"}})
	persona, err := authorize(ctx, nil, checker, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"dp_builder"}, checker.roles, "the run's pre-authenticated roles are checked")
	assert.Equal(t, "builder", persona, "the persona the connection was allowed as chooses its login role (#2403)")

	_, err = authorize(ctx, nil, checker, "ledger")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `application database "ledger" is not allowed for this run`)

	_, err = authorize(context.Background(), fixedAuthn{user: &middleware.UserInfo{Roles: []string{"r"}}}, checker, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"r"}, checker.roles)
	_, err = authorize(context.Background(), fixedAuthn{err: errors.New("x")}, checker, "shop")
	require.ErrorIs(t, err, errNoIdentity)
	_, err = authorize(context.Background(), nil, checker, "shop")
	require.ErrorIs(t, err, errNoIdentity)
	persona, err = authorize(context.Background(), nil, nil, "anything")
	require.NoError(t, err, "no persona check configured")
	assert.Empty(t, persona)
}

// A test's database is made with the test server's credentials, so a run
// allowed the connection but not its test_connection is refused (#2407).
func TestAppDB_ATestDatabaseNeedsTheTestServerToo(t *testing.T) {
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "shop"})
	t.Cleanup(func() { _ = pg.Close() })
	require.NoError(t, pg.AddConnection("shop", map[string]any{
		"host": "localhost", "database": "shop", "user": "u",
		"sslmode": "disable", "test_connection": "scratch",
	}))
	AppDB(func() []registry.Toolkit { return []registry.Toolkit{pg} }, fixedAuthn{err: errors.New("no token")}, &connChecker{})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })

	assert.Equal(t, "scratch", pg.TestServerOf("shop"))
	assert.Equal(t, "other", pg.TestServerOf("other"))
	ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{Roles: []string{"dp_builder"}})
	err := scriptsession.NewTestDatabases(ctx).Open("shop", nil)
	assert.ErrorContains(t, err, `"scratch" is not allowed for this run`)
}
