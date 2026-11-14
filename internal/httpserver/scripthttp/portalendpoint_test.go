package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/scriptlist"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
)

// listedBindings is a binding store holding a fixed set, answering a listing
// with the bindings of the scripts it names.
type listedBindings struct {
	epbinding.Store
	bindings []epbinding.Binding
	asked    epbinding.Filter
	err      error
}

func (l *listedBindings) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	l.asked = f
	if l.err != nil {
		return nil, 0, l.err
	}
	out := []epbinding.Binding{}
	for _, b := range l.bindings {
		for _, id := range f.ScriptIDs {
			if b.ScriptID == id {
				out = append(out, b)
			}
		}
	}
	return out, len(out), nil
}

func (*listedBindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{Binding: time.Time{}}, nil
}

// typesView is a scripthandler.Reader answering only the listing's question:
// each script's handler types.
type typesView struct {
	types map[string][]string
	asked []string
	err   error
}

func (*typesView) List(context.Context, scripthandler.Filter) ([]scripthandler.Handler, int, error) {
	return []scripthandler.Handler{}, 0, nil
}

func (*typesView) Get(context.Context, string) (scripthandler.Handler, error) {
	return scripthandler.Handler{}, scripthandler.ErrNotFound
}

func (v *typesView) TypesOf(_ context.Context, ids []string) (map[string][]string, error) {
	v.asked = ids
	return v.types, v.err
}

// listedRow is a listing row's handler fields as the wire carries them.
type listedRow struct {
	Script       struct{ ID string }   `json:"script"`
	Endpoints    []scriptlist.Endpoint `json:"endpoints"`
	HandlerTypes []string              `json:"handler_types"`
}

func listedRows(t *testing.T, rec *httptest.ResponseRecorder) map[string]listedRow {
	t.Helper()
	var out struct {
		Data []listedRow `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	byID := map[string]listedRow{}
	for _, row := range out.Data {
		byID[row.Script.ID] = row
	}
	return byID
}

// #2259, #2441, #2435: each listed script carries every endpoint it answers
// and its handler types, a script with none carries [] of each, and type=
// narrows the listing in the store.
func TestPortalListScripts_CarriesEachScriptsHandlers(t *testing.T) {
	store := portalStore()
	bindings := &listedBindings{bindings: []epbinding.Binding{
		{
			ScriptID: "script_2", Name: "carols-report", Path: "/{id}", AuthMode: epbinding.AuthBotCheck,
			BotCheck: "default", Enabled: false,
		},
		{ScriptID: "script_2", Name: "carols-report-archive", AuthMode: epbinding.AuthAuthenticated, Enabled: true},
	}}
	view := &typesView{types: map[string][]string{"script_2": {"endpoint", "schedule"}}}
	deps := portalDeps(store, nil, nil, carol)
	deps.Handlers = &scripthandler.Service{View: view, Endpoints: &epcmd.Handler{Store: bindings}}

	rec := servePortal(t, deps, "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	byID := listedRows(t, rec)
	require.Contains(t, byID, "script_1")
	assert.Equal(t, []scriptlist.Endpoint{}, byID["script_1"].Endpoints, "a script bound to no endpoint carries []")
	assert.Equal(t, []string{}, byID["script_1"].HandlerTypes, "a script with no handler carries []")
	assert.Equal(t, []scriptlist.Endpoint{
		{Name: "carols-report", Path: "/endpoints/carols-report/{id}", AuthMode: epbinding.AuthBotCheck, Enabled: false},
		{Name: "carols-report-archive", Path: "/endpoints/carols-report-archive", AuthMode: epbinding.AuthAuthenticated, Enabled: true},
	}, byID["script_2"].Endpoints)
	assert.Equal(t, []string{"endpoint", "schedule"}, byID["script_2"].HandlerTypes)
	assert.Len(t, bindings.asked.ScriptIDs, 2, "one read for the page's scripts")
	assert.Equal(t, 2*scripthandler.MaxPerKind, bindings.asked.Limit, "the limit leaves room for every endpoint each script may hold")
	assert.Len(t, view.asked, 2, "one read of the page's handler types")

	rec = servePortal(t, deps, "/api/v1/portal/scripts?type=schedule&type=none")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"schedule", "none"}, store.lastFilter.HandlerTypes)

	rec = servePortal(t, deps, "/api/v1/portal/scripts?type=yes")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `unknown type \"yes\"`)
	rec = servePortal(t, deps, "/api/v1/portal/scripts?endpoint=bound")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "endpoint= was removed")

	bindings.err, view.err = errors.New("down"), errors.New("down")
	rec = servePortal(t, deps, "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code, "a listing that cannot read the handlers is still a listing")
	byID = listedRows(t, rec)
	assert.Equal(t, []scriptlist.Endpoint{}, byID["script_2"].Endpoints)
	assert.Equal(t, []string{}, byID["script_2"].HandlerTypes)
}

// handlerRows is a typesView answering a listing with fixed rows.
type handlerRows struct {
	typesView
	rows []scripthandler.Handler
}

func (h *handlerRows) List(_ context.Context, f scripthandler.Filter) ([]scripthandler.Handler, int, error) {
	out := []scripthandler.Handler{}
	for _, r := range h.rows {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, r.ScriptID) {
			out = append(out, r)
		}
	}
	return out, len(out), nil
}

// TestHandlerRoutesAreMountedWithTheHandlerService: the portal's handler
// routes and Admin > Handlers are mounted where the deployment keeps
// handlers (#2441, #2435), the portal's with the caller the portal resolved.
func TestHandlerRoutesAreMountedWithTheHandlerService(t *testing.T) {
	view := &handlerRows{rows: []scripthandler.Handler{
		{ID: "sched_1", Kind: scripthandler.KindSchedule, Type: scripthandler.KindSchedule, ScriptID: "script_9", OwnerEmail: "carol@example.com"},
	}}
	svc := &scripthandler.Service{View: view, Scripts: portalStore()}

	deps := portalDeps(portalStore(), nil, nil, carol)
	deps.Handlers = svc
	rec := servePortal(t, deps, "/api/v1/portal/scripts/script_2/handlers")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"handlers":[]`, "the owner reads their script's handlers: none")

	signedOut := portalDeps(portalStore(), nil, nil, nil)
	signedOut.Handlers = svc
	rec = servePortal(t, signedOut, "/api/v1/portal/handlers/sched_1")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "a request the portal resolved no caller for is refused")

	none := portalDeps(portalStore(), nil, nil, carol)
	rec = servePortal(t, none, "/api/v1/portal/handlers/sched_1")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no handler service, no handler routes")

	admin := Deps{Scripts: portalStore(), Handlers: svc}
	rec = serveAdmin(t, admin, "/api/v1/admin/handlers?type=schedule")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"id":"sched_1"`)
	assert.Equal(t, http.StatusNotFound, serveAdmin(t, Deps{Scripts: portalStore()}, "/api/v1/admin/handlers").Code)
}
