package taskwiring

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// recordingLifecycle records what is registered on it.
type recordingLifecycle struct {
	components, drains int
}

func (l *recordingLifecycle) OnComponent(_, _ func(context.Context) error) { l.components++ }
func (l *recordingLifecycle) OnDrain(func(context.Context) error)          { l.drains++ }

// The section defaults on, keeps an export task seven days, and the
// extension is offered only where there is a database.
func TestConfig(t *testing.T) {
	off := false
	assert.True(t, Config{}.IsEnabled())
	assert.False(t, Config{Enabled: &off}.IsEnabled())
	assert.Equal(t, 7*24*time.Hour, Config{}.Retention())
	assert.Equal(t, 2*24*time.Hour, Config{RetentionDays: 2}.Retention())

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.True(t, Offered(Config{}, db))
	assert.False(t, Offered(Config{Enabled: &off}, db))
	assert.False(t, Offered(Config{}, nil))
}

// Where it is not offered, Install registers nothing and returns no
// middleware; a nil Handle is a no-op.
func TestInstallWhereNotOffered(t *testing.T) {
	life := &recordingLifecycle{}
	assert.Nil(t, Install(Deps{Lifecycle: life}))
	assert.Zero(t, life.components)

	var h *Handle
	assert.Nil(t, h.Middleware())
	require.NoError(t, h.Register(mcp.NewServer(&mcp.Implementation{Name: "s", Version: "1"}, nil)))
	require.NoError(t, h.Start(context.Background()))
	require.NoError(t, h.Drain(context.Background()))
	require.NoError(t, h.Stop(context.Background()))
}

// Where it is offered, Install registers the methods and the runner and
// returns the middleware; runs and exports answer under their prefixes.
func TestInstallWhereOffered(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	life := &recordingLifecycle{}
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "1"}, nil)
	mw := Install(Deps{DB: db, Server: server, Lifecycle: life, Runs: func() *scriptlayer.RunTasks { return nil }})
	require.NotNil(t, mw)
	assert.Equal(t, 1, life.components)
	assert.Equal(t, 1, life.drains)

	h := New(Deps{DB: db, Runs: func() *scriptlayer.RunTasks { return nil }})
	require.NotNil(t, h)
	assert.NotNil(t, h.sources.For("dpx_1"), "runs answer under the run id prefix")
	assert.NotNil(t, h.sources.For("dpt_1"), "exports answer under the call task prefix")
	require.NoError(t, h.Start(context.Background()))
	require.NoError(t, h.Drain(context.Background()))
	require.NoError(t, h.Stop(context.Background()))
	assert.Nil(t, New(Deps{DB: db}).sources.For("dpx_1"), "no script layer, no run tasks")
}

// Before the script layer exists, no run is a task.
func TestLateRunsBeforeTheScriptLayer(t *testing.T) {
	runs := lateRuns{runs: func() *scriptlayer.RunTasks { return nil }}
	_, err := runs.Get(context.Background(), "dpx_1")
	require.ErrorIs(t, err, mcptask.ErrNotFound)
	require.ErrorIs(t, runs.Cancel(context.Background(), "dpx_1"), mcptask.ErrNotFound)
}

// The tasks/* caller is identified the way a completion request is: by the
// authenticator, with the persona its roles resolve to.
func TestResolverIdentifiesTheCaller(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "admin", Roles: []string{"dp_admin"}}))
	resolve := resolver(Deps{Authenticator: staticAuth{}, Personas: reg, AdminPersona: "admin"})
	ctx := resolve(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})
	pc := middleware.GetPlatformContext(ctx)
	require.NotNil(t, pc)
	assert.Equal(t, "root", pc.UserID)
	assert.True(t, pc.IsAdmin)

	none := resolver(Deps{})(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})
	assert.Nil(t, middleware.GetPlatformContext(none), "no authenticator, no caller")
}

type staticAuth struct{}

func (staticAuth) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: "root", Roles: []string{"dp_admin"}}, nil
}

// run_script is its own record; the three exports run apart from the request.
func TestTools(t *testing.T) {
	assert.Equal(t, map[string]mcptask.Mode{
		"run_script": mcptask.Native, "trino_export": mcptask.Detached,
		"api_export": mcptask.Detached, "graphql_export": mcptask.Detached,
	}, Tools())
}
