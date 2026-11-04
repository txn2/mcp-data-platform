package appdbwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// other is a toolkit of another kind.
type other struct{ registry.Toolkit }

func pgKit(t *testing.T) *postgreskit.Toolkit {
	t.Helper()
	multi, err := postgreskit.ParseMultiConfig("shop", map[string]map[string]any{})
	require.NoError(t, err)
	tk := postgreskit.NewMulti(multi)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

func TestAssemble(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var tks []registry.Toolkit
	svc, cfg := assemble(db, nil, "https://p.example.com/", func() []registry.Toolkit { return tks })
	require.NotNil(t, svc)
	assert.NotNil(t, cfg.Store)
	assert.Same(t, svc, cfg.Watch)
	assert.Nil(t, cfg.Kit(), "no postgres toolkit is registered")

	tks = []registry.Toolkit{other{}, pgKit(t)}
	assert.NotNil(t, cfg.Kit(), "a toolkit the registry gains later is found")
	assert.Nil(t, live(func() []registry.Toolkit { return []registry.Toolkit{other{}} }))
}

func TestInstall(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	svc, cfg := assemble(db, nil, "", func() []registry.Toolkit { return nil })
	pg := pgKit(t)
	assert.NotPanics(t, func() { Install([]registry.Toolkit{other{}, pg}, cfg.Store, svc) })
}

func TestMount_WithoutAPlatform(t *testing.T) {
	assert.NotPanics(t, func() { Mount(context.Background(), http.NewServeMux(), nil, nil, Admin{}) })
}

func TestPageURL(t *testing.T) {
	assert.Equal(t, "https://p.example.com/portal/admin/connections/appdb/shop%20db", pageURL("https://p.example.com/")("shop db"))
	assert.Empty(t, pageURL("")("shop"))
}

func TestHooks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	svc, cfg := assemble(db, nil, "", func() []registry.Toolkit { return nil })
	pg := pgKit(t)

	mock.ExpectQuery(`FROM appdb_migrations`).WillReturnRows(sqlmock.NewRows([]string{"x"}))
	res := driftCheck(pg, cfg.Store)(context.Background(), "shop", "public")
	assert.NotNil(t, res)

	mock.ExpectQuery(`platform_settings`).WillReturnError(sqlmock.ErrCancelled)
	got := findings(pg, svc)(context.Background(), "nope", "public", nil)
	assert.Empty(t, got, "an unknown connection has nothing to report")
}

func TestStart(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	installed := false
	mux := http.NewServeMux()
	stop := start(context.Background(), mux, deps{
		db: db, all: func() []registry.Toolkit { return []registry.Toolkit{pgKit(t)} },
		gauges: func(func(ctx context.Context) []observability.AppDBHealthSample) { installed = true },
		wrap:   func(h http.Handler) http.Handler { return h },
	})
	stop()
	assert.True(t, installed, "the gauges are installed")
	_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/appdb-health/settings", http.NoBody))
	assert.NotEmpty(t, pattern, "the admin routes are mounted")
}

func TestWatchKit(t *testing.T) {
	assert.Nil(t, watchKit(func() []registry.Toolkit { return nil })(), "nil, never a typed nil")
	pg := pgKit(t)
	assert.NotNil(t, watchKit(func() []registry.Toolkit { return []registry.Toolkit{pg} })())
}
