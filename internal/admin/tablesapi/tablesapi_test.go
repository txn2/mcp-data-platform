package tablesapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tablereconcile"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
)

type fakeRegs struct {
	filter tableregister.Filter
	page   []tableregister.Registration
	err    error
}

func (f *fakeRegs) List(_ context.Context, filter tableregister.Filter) ([]tableregister.Registration, int, error) {
	f.filter = filter
	return f.page, len(f.page), f.err
}

type fakeRec struct {
	connection string
	caller     tableregister.Caller
	items      []tablereconcile.RepairItem
	err        error
}

func (f *fakeRec) Reconcile(_ context.Context, connection string) (*tablereconcile.Reconciliation, error) {
	f.connection = connection
	if f.err != nil {
		return nil, f.err
	}
	return &tablereconcile.Reconciliation{
		Connection: connection, Orphans: []tablereconcile.Orphan{{Table: "left_over", Provable: true}},
		Missing: []tablereconcile.Missing{}, Drifted: []tablereconcile.Drifted{},
	}, nil
}

func (f *fakeRec) Repair(_ context.Context, caller tableregister.Caller, connection string, items []tablereconcile.RepairItem) ([]tablereconcile.RepairOutcome, error) {
	f.connection, f.caller, f.items = connection, caller, items
	if f.err != nil {
		return nil, f.err
	}
	out := make([]tablereconcile.RepairOutcome, 0, len(items))
	for _, it := range items {
		out = append(out, tablereconcile.RepairOutcome{RepairItem: it, Done: true})
	}
	return out, nil
}

func newMux(regs Registrations, rec Reconciler) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Registrations: regs, Reconciler: rec,
		Caller: func(*http.Request) tableregister.Caller {
			return tableregister.Caller{Email: "admin@example.com", IsAdmin: true}
		},
	})
	return mux
}

func do(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	return rec
}

// TestRegistrations: every filter the route takes reaches the listing, a
// connection narrows it, and no registration is an empty list.
func TestRegistrations(t *testing.T) {
	regs := &fakeRegs{}
	mux := newMux(regs, nil)
	rec := do(mux, http.MethodGet, registrationsPath+"?persona=analyst&source_kind=resource&source_id=r1&q=sales&limit=5&offset=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"registrations":[],"total":0}`, rec.Body.String())
	assert.Equal(t, tableregister.Filter{
		AllConnections: true, SourceKind: "resource", SourceID: "r1", Persona: "analyst", Query: "sales", Limit: 5, Offset: 10,
	}, regs.filter)

	do(mux, http.MethodGet, registrationsPath+"?connection=scratch&limit=x", "")
	assert.False(t, regs.filter.AllConnections)
	assert.Equal(t, []string{"scratch"}, regs.filter.Connections)
	assert.Zero(t, regs.filter.Limit)

	regs.err = errors.New("down")
	assert.Equal(t, http.StatusInternalServerError, do(mux, http.MethodGet, registrationsPath, "").Code)

	assert.Equal(t, http.StatusNotFound, do(mux, http.MethodGet, reconcilePath+"?connection=x", "").Code,
		"no reconciler, no reconcile route")
}

// TestReconcileRoute: the connection is required, the reconciliation is
// returned, and a connection with no scratch target is the caller's error.
func TestReconcileRoute(t *testing.T) {
	r := &fakeRec{}
	mux := newMux(&fakeRegs{}, r)
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodGet, reconcilePath, "").Code)

	rec := do(mux, http.MethodGet, reconcilePath+"?connection=scratch", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"table":"left_over"`)
	assert.Equal(t, "scratch", r.connection)

	r.err = tableregister.ErrNoScratchTarget
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodGet, reconcilePath+"?connection=x", "").Code)
	r.err = errors.New("coordinator down")
	rec = do(mux, http.MethodGet, reconcilePath+"?connection=x", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "coordinator down")
}

// TestRepairRoute: the repairs reach the reconciler with the acting
// administrator; a body that names no connection or no repair, carries an
// unknown field, or asks for too many is refused.
func TestRepairRoute(t *testing.T) {
	r := &fakeRec{}
	mux := newMux(&fakeRegs{}, r)
	rec := do(mux, http.MethodPost, reconcilePath,
		`{"connection":"scratch","repair":[{"action":"drop","table":"left_over"},{"action":"forget","registration_id":"reg_1"}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"done":true`)
	assert.Equal(t, "admin@example.com", r.caller.Email)
	assert.Len(t, r.items, 2)

	for name, body := range map[string]string{
		"no connection": `{"repair":[{"action":"drop","table":"t"}]}`,
		"no repair":     `{"connection":"scratch","repair":[]}`,
		"unknown field": `{"connection":"scratch","repair":[],"force":true}`,
		"two values":    `{"connection":"scratch","repair":[{"action":"drop"}]} {}`,
		"too many":      `{"connection":"scratch","repair":[` + strings.Repeat(`{"action":"drop","table":"t"},`, maxRepairs) + `{"action":"drop","table":"t"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPost, reconcilePath, body).Code)
		})
	}

	r.err = tableregister.ErrNoScratchTarget
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPost, reconcilePath, `{"connection":"x","repair":[{"action":"drop","table":"t"}]}`).Code)
}

func TestRegisterWithNothingMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{})
	assert.Equal(t, http.StatusNotFound, do(mux, http.MethodGet, registrationsPath, "").Code)
}
