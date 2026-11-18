package scriptevents

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appenv"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// envStore is a fakeStore whose handlers name an environment.
type envStore struct {
	*fakeStore
	env string
	err error
}

func (e envStore) HandlerEnvironment(context.Context, string, string) (string, error) {
	return e.env, e.err
}

type pins map[string]int

func (p pins) ForScript(_ context.Context, _, env string) (*appenv.Script, error) {
	v, ok := p[env]
	if !ok {
		return nil, appenv.ErrNoEnvironment
	}
	return &appenv.Script{OK: true, Environment: env, Version: v}, nil
}

// An event run of a handler naming an environment runs that environment's
// pinned version and records the environment (#2696).
func TestStored_RunsInTheHandlersEnvironment(t *testing.T) {
	base := &fakeStore{scripts: map[string][]string{"shop": {"s1"}}}
	scripts := fakeScripts{scripts: map[string]*script.Script{"s1": liveScript("s1")}, versions: map[string]*script.Version{"s1": {ID: "v1", Version: 1}}}
	e := &Enqueuer{Store: envStore{fakeStore: base, env: "dev"}, Bindings: &Bindings{Reader: base}, Scripts: scripts, Environments: pins{"dev": 1}}
	e.Stored(t.Context(), "shop", events("ev1"))
	require.Len(t, base.enqueued, 1)
	assert.Equal(t, "dev", base.enqueued[0][0].Environment)

	base.enqueued = nil
	e.Environments = pins{}
	e.Stored(t.Context(), "shop", events("ev2"))
	assert.Empty(t, base.enqueued, "an environment the application lacks enqueues nothing")
	e.Store = envStore{fakeStore: base, err: errors.New("down")}
	e.Stored(t.Context(), "shop", events("ev3"))
	assert.Empty(t, base.enqueued)
}

func TestHandlerEnvironmentStore(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	read := regexp.QuoteMeta("SELECT environment FROM script_webhook_handlers")
	mock.ExpectQuery(read).WithArgs("s1", "shop").WillReturnRows(sqlmock.NewRows([]string{"environment"}).AddRow("dev"))
	env, err := s.HandlerEnvironment(ctx, "s1", "shop")
	require.NoError(t, err)
	assert.Equal(t, "dev", env)
	mock.ExpectQuery(read).WillReturnError(sql.ErrNoRows)
	env, err = s.HandlerEnvironment(ctx, "s1", "shop")
	require.NoError(t, err)
	assert.Empty(t, env)
	mock.ExpectQuery(read).WillReturnError(errors.New("down"))
	_, err = s.HandlerEnvironment(ctx, "s1", "shop")
	require.Error(t, err)

	update := regexp.QuoteMeta("SET environment = $2")
	mock.ExpectExec(update).WithArgs("wh1", "dev", "admin").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetHandlerEnvironment(ctx, "wh1", "dev", "admin"))
	mock.ExpectExec(update).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.SetHandlerEnvironment(ctx, "gone", "dev", "admin"), ErrHandlerNotFound)
	mock.ExpectExec(update).WillReturnError(errors.New("down"))
	require.Error(t, s.SetHandlerEnvironment(ctx, "wh1", "dev", "admin"))
	require.NoError(t, mock.ExpectationsWereMet())
}
