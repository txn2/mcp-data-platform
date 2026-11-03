package migrationsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// fakeKit is a toolkit serving the application database "shop".
type fakeKit struct {
	registry.Toolkit
	err    error
	schema string
}

func (*fakeKit) HasConnection(name string) bool { return name == "shop" }

func (f *fakeKit) Report(_ context.Context, connection, schema string) (*postgreskit.SchemaReport, error) {
	f.schema = schema
	if f.err != nil {
		return nil, f.err
	}
	return &postgreskit.SchemaReport{
		Connection: connection, Schema: schema, CurrentVersion: 2,
		Migrations: []postgreskit.MigrationState{}, Drift: []postgreskit.Drift{}, Events: []postgreskit.MigrationEvent{},
	}, nil
}

func (f *fakeKit) PendingPlan(_ context.Context, _, schema string) (*postgreskit.Plan, error) {
	f.schema = schema
	if f.err != nil {
		return nil, f.err
	}
	return &postgreskit.Plan{Migrations: []int{3}, Changed: []string{"public.orders.note"}, Locks: []postgreskit.Lock{}, Unplanned: []int{}}, nil
}

func serve(t *testing.T, kit *fakeKit, method, path string) (rec *httptest.ResponseRecorder, body map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Config{Toolkits: func() []registry.Toolkit { return []registry.Toolkit{kit} }})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

const base = "/api/v1/admin/connection-instances/appdb/"

func TestListAndPlan(t *testing.T) {
	kit := &fakeKit{}
	rec, body := serve(t, kit, http.MethodGet, base+"shop/migrations")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(2), body["current_version"])
	assert.Equal(t, postgreskit.DefaultSchema, kit.schema, "no schema is public")

	rec, body = serve(t, kit, http.MethodPost, base+"shop/migrations/plan?schema=billing")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "billing", kit.schema)
	assert.Equal(t, []any{"public.orders.note"}, body["changed"])
}

func TestFailures(t *testing.T) {
	rec, _ := serve(t, &fakeKit{}, http.MethodGet, base+"nope/migrations")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = serve(t, &fakeKit{}, http.MethodPost, base+"nope/migrations/plan")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	for err, want := range map[error]int{
		&postgreskit.RefusedError{Code: postgreskit.CodeSchemaDrift}: http.StatusBadRequest,
		postgreskit.ErrNoMigrationStore:                              http.StatusNotFound,
		errors.New("connection refused"):                             http.StatusServiceUnavailable,
	} {
		rec, _ = serve(t, &fakeKit{err: err}, http.MethodGet, base+"shop/migrations")
		assert.Equal(t, want, rec.Code, err.Error())
		rec, _ = serve(t, &fakeKit{err: err}, http.MethodPost, base+"shop/migrations/plan")
		assert.Equal(t, want, rec.Code, err.Error())
	}
}

func TestNoRegistryNoRoutes(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Config{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"shop/migrations", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
