package appdbapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/appdbconsole"
	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/appdbwatch"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// kit is a postgres toolkit serving "shop".
type kit struct {
	maintErr   error
	queryErr   error
	statusErr  error
	signalRows [][]any
	consoleTx  *tx
}

func (*kit) HasConnection(name string) bool { return name == "shop" }
func (*kit) ListConnections() []toolkit.ConnectionDetail {
	return []toolkit.ConnectionDetail{{Name: "shop"}}
}

func (*kit) Describe(context.Context, string, string) (*postgres.Description, error) {
	return &postgres.Description{}, nil
}

func (*kit) DescribeBuilt(context.Context, string, string, []postgres.Migration) (*postgres.Description, error) {
	return &postgres.Description{}, nil
}

func (k *kit) Status(context.Context, string, string, []postgres.Migration) (*postgres.LedgerStatus, error) {
	return &postgres.LedgerStatus{}, k.statusErr
}

func (*kit) ReadTx(context.Context, string) (appdbdrift.Tx, error) { return &tx{}, nil }

func (*kit) Apply(_ context.Context, _ postgres.ApplyOptions, ms []postgres.Migration) ([]postgres.Migration, error) {
	return ms, nil
}

func (k *kit) RunMaintenance(context.Context, string, string, time.Duration, time.Duration) error {
	return k.maintErr
}

func (k *kit) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	if k.queryErr != nil {
		return nil, k.queryErr
	}
	if strings.Contains(s.SQL, "pg_cancel_backend") || strings.Contains(s.SQL, "pg_terminate_backend") {
		return &postgres.Result{Rows: k.signalRows}, nil
	}
	return &postgres.Result{}, nil
}

func (*kit) ConnectionRoles(string) []string { return []string{"orders_app"} }

func (k *kit) ConsoleTx(context.Context, string, bool) (appdbconsole.Tx, error) {
	if k.consoleTx == nil {
		return nil, errors.New("appdb: connection \"shop\" could not reach its database")
	}
	return k.consoleTx, nil
}

type tx struct{ fail error }

func (x *tx) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	if x.fail != nil {
		return nil, x.fail
	}
	if strings.Contains(s.SQL, "SELECT kind, name FROM") {
		return &postgres.Result{}, nil
	}
	return &postgres.Result{Command: "SELECT", Columns: []postgres.Column{{Name: "n"}}, Rows: [][]any{{int64(1)}}}, nil
}
func (*tx) Commit(context.Context) error { return nil }
func (*tx) Rollback()                    {}

type store struct{ migrations []postgres.Migration }

func (s *store) List(context.Context, string, string) ([]postgres.Migration, error) {
	return s.migrations, nil
}

func (s *store) Put(_ context.Context, m postgres.Migration) (bool, error) {
	s.migrations = append(s.migrations, m)
	return false, nil
}
func (*store) Record(context.Context, postgres.MigrationEvent) error { return nil }

type audits struct{ events []middleware.AuditEvent }

func (a *audits) Log(_ context.Context, e middleware.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}

type settings struct {
	stored *settingsjson.Stored[appdbwatch.Settings]
}

func (s *settings) Get(context.Context) (settingsjson.Stored[appdbwatch.Settings], error) {
	if s.stored == nil {
		return settingsjson.Stored[appdbwatch.Settings]{}, settingsjson.ErrNotFound
	}
	return *s.stored, nil
}

func (s *settings) Set(_ context.Context, v appdbwatch.Settings, by string) error {
	s.stored = &settingsjson.Stored[appdbwatch.Settings]{Value: v, UpdatedBy: by}
	return nil
}

type rig struct {
	mux    *http.ServeMux
	kit    *kit
	audit  *audits
	mock   sqlmock.Sqlmock
	called bool
}

func newRig(t *testing.T, k *kit, st *store) *rig {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	watch := appdbwatch.New(appdbwatch.Deps{DB: db, Settings: &settings{}, Kit: func() appdbwatch.Kit { return nil }})
	r := &rig{mux: http.NewServeMux(), kit: k, audit: &audits{}, mock: mock}
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.called = true
			h.ServeHTTP(w, req)
		})
	}
	Register(r.mux, wrap, Config{
		Kit: func() Kit { return k }, Store: st, Watch: watch, Audit: r.audit,
		Actor: func(*http.Request) string { return "admin@example.com" },
	})
	return r
}

func (r *rig) do(method, path, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	rec = httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	out = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

const shopBase = "/api/v1/admin/connection-instances/appdb/shop"

func TestEveryRouteIsWrapped(t *testing.T) {
	r := newRig(t, &kit{}, &store{})
	r.do(http.MethodGet, "/api/v1/admin/appdb-health/settings", "")
	assert.True(t, r.called, "the admin wrapper guards the route")
	rec, _ := r.do(http.MethodGet, "/api/v1/admin/connection-instances/appdb/nope/health", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, shopBase+"/health", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "nothing is mounted without its dependencies")
}

func TestHealthAndHistory(t *testing.T) {
	r := newRig(t, &kit{}, &store{})
	rec, body := r.do(http.MethodGet, shopBase+"/health", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "shop", body["connection"])

	r.mock.ExpectQuery(`FROM appdb_health_samples`).WillReturnRows(sqlmock.NewRows([]string{"connection", "sampled_at", "summary"}))
	rec, body = r.do(http.MethodGet, shopBase+"/health/history?days=3", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(3), body["days"])
	assert.Equal(t, []any{}, body["points"])
	rec, _ = r.do(http.MethodGet, shopBase+"/health/history?days=0", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	r.mock.ExpectQuery(`FROM appdb_health_samples`).WillReturnError(errors.New("down"))
	rec, _ = r.do(http.MethodGet, shopBase+"/health/history", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestDriftAndAdopt(t *testing.T) {
	st := &store{}
	r := newRig(t, &kit{}, st)
	rec, body := r.do(http.MethodGet, shopBase+"/drift", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []any{}, body["schemas"])
	rec, body = r.do(http.MethodGet, shopBase+"/drift?schema=app", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, body["schemas"], 1)

	rec, body = r.do(http.MethodPost, shopBase+"/adopt", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, body["adopted"])
	assert.Equal(t, "public", body["schema"])
	rec, _ = r.do(http.MethodPost, shopBase+"/adopt?schema=public", "")
	assert.Equal(t, http.StatusConflict, rec.Code, "a managed schema is not adopted twice")

	r = newRig(t, &kit{statusErr: errors.New("appdb: connection \"shop\" could not reach its database")}, &store{})
	rec, _ = r.do(http.MethodPost, shopBase+"/adopt", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Len(t, r.audit.events, 1)
	assert.Equal(t, toolAdopt, r.audit.events[0].ToolName)
	assert.False(t, r.audit.events[0].Success)
}

func TestMaintenance(t *testing.T) {
	r := newRig(t, &kit{}, &store{})
	rec, body := r.do(http.MethodPost, shopBase+"/maintenance", `{"command":"analyze","table":"orders","timeout_seconds":5}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, `ANALYZE "orders"`, body["statement"])
	rec, body = r.do(http.MethodPost, shopBase+"/maintenance", `{"command":"create_index","table":"orders"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, body["detail"], "manage_schema")
	rec, _ = r.do(http.MethodPost, shopBase+"/maintenance", `not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	r = newRig(t, &kit{maintErr: &postgres.FailedError{Code: postgres.CodeUnavailable, Summary: "unreachable"}}, &store{})
	rec, _ = r.do(http.MethodPost, shopBase+"/maintenance", `{"command":"vacuum"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	r = newRig(t, &kit{maintErr: &postgres.FailedError{Code: postgres.CodeStatementFailed, Summary: "SQLSTATE 55P03"}}, &store{})
	rec, _ = r.do(http.MethodPost, shopBase+"/maintenance", `{"command":"vacuum"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, toolMaintenance, r.audit.events[0].ToolName)
}

func TestSignal(t *testing.T) {
	r := newRig(t, &kit{signalRows: [][]any{{true}}}, &store{})
	rec, body := r.do(http.MethodPost, shopBase+"/backends/42/terminate", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, body["sent"])
	rec, _ = r.do(http.MethodPost, shopBase+"/backends/abc/terminate", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = r.do(http.MethodPost, shopBase+"/backends/42/kill", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, toolSignal, r.audit.events[len(r.audit.events)-1].ToolName)
}

// The console's audit row is the SQL text and each parameter's shape, never a
// bound value, and never what the server said about a failure beyond its
// value-free summary.
func TestConsole(t *testing.T) {
	r := newRig(t, &kit{consoleTx: &tx{}}, &store{})
	rec, body := r.do(http.MethodPost, shopBase+"/console", `{"sql":"SELECT $1","params":["canary-bound-value"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, body["read_only"])
	require.Len(t, r.audit.events, 1)
	ev := r.audit.events[0]
	assert.Equal(t, SourceConsole, ev.Source)
	assert.Equal(t, "shop", ev.Connection)
	raw, _ := json.Marshal(ev)
	assert.Contains(t, string(raw), "SELECT $1")
	assert.Contains(t, string(raw), `"type":"string"`)
	assert.NotContains(t, string(raw), "canary-bound-value")

	r = newRig(t, &kit{consoleTx: &tx{fail: &postgres.FailedError{
		Code: postgres.CodeStatementFailed, Summary: "SQLSTATE 23505",
		Statement: &postgres.StatementError{},
	}}}, &store{})
	rec, body = r.do(http.MethodPost, shopBase+"/console", `{"sql":"INSERT INTO t VALUES ($1)","params":["canary-bound-value"],"read_only":false}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, body["error"])
	ev = r.audit.events[0]
	assert.False(t, ev.Success)
	assert.Equal(t, "SQLSTATE 23505", ev.ErrorMessage)
	raw, _ = json.Marshal(ev)
	assert.NotContains(t, string(raw), "canary-bound-value")

	r = newRig(t, &kit{}, &store{})
	rec, _ = r.do(http.MethodPost, shopBase+"/console", `{"sql":"SELECT 1"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec, _ = r.do(http.MethodPost, shopBase+"/console", `{"sql":"SELECT 1; SELECT 2","params":[1]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSettingsRoutes(t *testing.T) {
	r := newRig(t, &kit{}, &store{})
	rec, body := r.do(http.MethodGet, "/api/v1/admin/appdb-health/settings", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(appdbwatch.DefaultIntervalSeconds), body["interval_seconds"])
	rec, body = r.do(http.MethodPut, "/api/v1/admin/appdb-health/settings", `{"channels":["ops"],"interval_seconds":60}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "admin@example.com", body["updated_by"])
	rec, _ = r.do(http.MethodPut, "/api/v1/admin/appdb-health/settings", `{"interval_seconds":1}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = r.do(http.MethodPut, "/api/v1/admin/appdb-health/settings", `{`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWrapAndHelpers(t *testing.T) {
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{})
	require.NoError(t, err)
	w := Wrap(postgres.NewMulti(multi))
	_, err = w.ReadTx(context.Background(), "nope")
	require.Error(t, err)
	_, err = w.ConsoleTx(context.Background(), "nope", true)
	require.Error(t, err)
	assert.NoError(t, cmpErr(nil, nil))
}

type brokenSettings struct{}

func (brokenSettings) Get(context.Context) (settingsjson.Stored[appdbwatch.Settings], error) {
	return settingsjson.Stored[appdbwatch.Settings]{}, errors.New("down")
}

func (brokenSettings) Set(context.Context, appdbwatch.Settings, string) error {
	return errors.New("down")
}

func TestSettingsUnreadable(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	watch := appdbwatch.New(appdbwatch.Deps{DB: db, Settings: brokenSettings{}, Kit: func() appdbwatch.Kit { return nil }})
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{Kit: func() Kit { return &kit{} }, Store: &store{}, Watch: watch})
	for _, path := range []string{shopBase + "/health", "/api/v1/admin/appdb-health/settings"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
		assert.Equal(t, http.StatusInternalServerError, rec.Code, path)
	}
}

// A connection another replica saved is taken on before the route looks it
// up, so the request is not answered 404 ahead of the reload bus.
func TestAConnectionIsTakenOnBeforeItIsLookedUp(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	watch := appdbwatch.New(appdbwatch.Deps{DB: db, Settings: &settings{}, Kit: func() appdbwatch.Kit { return nil }})
	var asked []string
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Kit: func() Kit { return &kit{} }, Store: &store{}, Watch: watch,
		TakeOn: func(_ context.Context, kind, name string) { asked = append(asked, kind+"/"+name) },
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, shopBase+"/drift", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"appdb/shop"}, asked)
}
