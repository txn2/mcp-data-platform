package handlerhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	endpointSource = "def handle(req):\n    return {\"json\": req}\n"
	mainSource     = "def main():\n    print(1)\n"
)

// rig is the portal and admin routes over a world, with the caller named by
// the X-User header (X-Admin: yes for an administrator, none for no caller).
type rig struct {
	w   *world
	svc *scripthandler.Service
	mux *http.ServeMux
}

func newRig() *rig {
	w := newWorld()
	w.scripts["s1"] = &script.Script{ID: "s1", Name: "daily", OwnerEmail: "jane@example.com", Source: mainSource, Enabled: true, Status: script.StatusActive}
	w.scripts["s2"] = &script.Script{ID: "s2", Name: "lookup", OwnerEmail: "jane@example.com", Source: endpointSource, Enabled: true, Status: script.StatusActive}
	g := &rig{w: w, svc: w.service(), mux: http.NewServeMux()}
	h := New(Deps{
		Handlers: g.svc, Scripts: scripts{w},
		Owned: func(rw http.ResponseWriter, r *http.Request) (string, string, bool) {
			id := r.PathValue("id")
			sc := w.scripts[id]
			email := r.Header.Get("X-User")
			if id != "broken" && id != "gone" && (sc == nil || (sc.OwnerEmail != email && r.Header.Get("X-Admin") != "yes")) {
				http.Error(rw, "not yours", http.StatusForbidden)
				return "", "", false
			}
			return id, email, true
		},
		Caller: func(r *http.Request) (scripthandler.Caller, bool) {
			email := r.Header.Get("X-User")
			return scripthandler.Caller{Email: email, Admin: r.Header.Get("X-Admin") == "yes"}, email != ""
		},
	})
	h.Register(g.mux, func(next http.Handler) http.Handler { return next })
	h.RegisterAdmin(g.mux, "/api/v1/admin", func(next http.Handler) http.Handler { return next })
	return g
}

const (
	jane  = "jane@example.com"
	bob   = "bob@example.com"
	admin = "admin@example.com"
)

func (g *rig) do(t *testing.T, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("X-User", user)
	}
	if user == admin {
		req.Header.Set("X-Admin", "yes")
	}
	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, req)
	return rec
}

func fields(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// add posts a handler and answers its id.
func (g *rig) add(t *testing.T, scriptID, body, user string) string {
	t.Helper()
	rec := g.do(t, http.MethodPost, "/api/v1/portal/scripts/"+scriptID+"/handlers", body, user)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id, _ := fields(t, rec)["handler"].(string)
	require.NotEmpty(t, id)
	return id
}

// TestAdd_EachKind is POST /portal/scripts/{id}/handlers (#2441, #2434): the
// owner adds a schedule and a webhook for their form's source, an
// administrator adds an endpoint, and each refusal answers its status.
func TestAdd_EachKind(t *testing.T) {
	g := newRig()
	path := "/api/v1/portal/scripts/s1/handlers"

	morning := g.add(t, "s1", `{"kind":"schedule","cron":"0 7 * * 1-5","timezone":"America/Los_Angeles"}`, jane)
	evening := g.add(t, "s1", `{"kind":"schedule","cron":"0 19 * * *"}`, jane)
	assert.NotEqual(t, morning, evening, "an owner adds a second schedule and sees both")
	assert.Len(t, g.w.schedules, 2)
	assert.Equal(t, "America/Los_Angeles", g.w.schedules[morning].Timezone)

	g.add(t, "s1", `{"kind":"webhook","event_source":"form-daily"}`, jane)
	g.add(t, "s2", `{"kind":"endpoint","endpoint":"orders","auth_mode":"authenticated","timeout_seconds":12}`, admin)
	for _, b := range g.w.bindings {
		assert.Equal(t, 12, b.TimeoutSeconds)
	}

	cases := []struct {
		name, body, user string
		status           int
		want             string
	}{
		{"the webhook rule", `{"kind":"webhook","event_source":"shop"}`, jane, http.StatusForbidden, "only an administrator"},
		{
			"an owner's endpoint on a script that is not a form", `{"kind":"endpoint","endpoint":"x","auth_mode":"authenticated"}`, jane,
			http.StatusForbidden, "an administrator binds an endpoint that is not a form",
		},
		{
			"an endpoint on a script with no handle(req)", `{"kind":"endpoint","endpoint":"x","auth_mode":"authenticated"}`, admin,
			http.StatusBadRequest, "defines no handle(req)",
		},
		{"an unknown kind", `{"kind":"rule"}`, jane, http.StatusBadRequest, "unknown handler kind"},
		{"a cadence that never fires", `{"kind":"schedule","cron":"every tuesday"}`, jane, http.StatusBadRequest, "not a cron expression"},
		{"a repeated cadence", `{"kind":"schedule","cron":"0 19 * * *"}`, jane, http.StatusBadRequest, "already has schedule handler " + evening},
		{"a body that is no handler", `nope`, jane, http.StatusBadRequest, "the body must be a handler"},
		{"another person's script", `{"kind":"schedule","cron":"@daily"}`, bob, http.StatusForbidden, "not yours"},
		{"no caller", `{"kind":"schedule","cron":"@daily"}`, "", http.StatusUnauthorized, "authentication required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := g.do(t, http.MethodPost, path, tc.body, tc.user)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}

	rec := g.do(t, http.MethodPost, "/api/v1/portal/scripts/s2/handlers", `{"kind":"endpoint","endpoint":"x","auth_mode":"authenticated"}`, jane)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the owner of an endpoint script that is not a form does not publish it")

	rec = g.do(t, http.MethodPost, "/api/v1/portal/scripts/gone/handlers", `{"kind":"schedule","cron":"@daily"}`, jane)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = g.do(t, http.MethodPost, "/api/v1/portal/scripts/broken/handlers", `{"kind":"schedule","cron":"@daily"}`, jane)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a script that cannot be read is not found")
}

// TestAdd_AStoreFailureIsThePlatformsAndIsNotRepeated: a failed write is 500
// with a sentence of the platform's, never the store's words.
func TestAdd_AStoreFailureIsThePlatformsAndIsNotRepeated(t *testing.T) {
	g := newRig()
	g.w.scheduleErr = errors.New("pq: password authentication failed for user platform")
	rec := g.do(t, http.MethodPost, "/api/v1/portal/scripts/s1/handlers", `{"kind":"schedule","cron":"@daily"}`, jane)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "the handler could not be read or written")
	assert.NotContains(t, rec.Body.String(), "password authentication")
}

// TestList_AScriptsHandlersAndWhatAddingOneOffers is GET
// /portal/scripts/{id}/handlers.
func TestList_AScriptsHandlersAndWhatAddingOneOffers(t *testing.T) {
	g := newRig()
	g.w.sources = []whsource.Source{
		{Name: "shop"},
		{Name: "form-daily", Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "s1"}}},
	}
	path := "/api/v1/portal/scripts/s1/handlers"

	rec := g.do(t, http.MethodGet, path, "", jane)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	empty := fields(t, rec)
	assert.Equal(t, []any{}, empty["handlers"], "no handler is [] and never null")
	assert.InDelta(t, scripthandler.MaxPerKind, empty["max_per_kind"], 0)
	assert.Equal(t, []any{"form-daily"}, empty["sources"], "the owner is offered their own form's source")
	offers, _ := empty["add"].([]any)
	require.Len(t, offers, len(scripthandler.Kinds()))
	endpointOffer, _ := offers[0].(map[string]any)
	assert.Equal(t, false, endpointOffer["allowed"])
	assert.Contains(t, empty, "endpoint", "what adding an endpoint offers")

	g.add(t, "s1", `{"kind":"schedule","cron":"@daily"}`, jane)
	g.add(t, "s1", `{"kind":"webhook","event_source":"form-daily"}`, jane)
	listed := fields(t, g.do(t, http.MethodGet, path, "", jane))
	items, _ := listed["handlers"].([]any)
	require.Len(t, items, 2)
	types := make([]any, 0, len(items))
	for _, it := range items {
		row, ok := it.(map[string]any)
		require.True(t, ok)
		types = append(types, row["type"])
	}
	assert.ElementsMatch(t, []any{scripthandler.KindSchedule, scripthandler.KindWebhook}, types)

	byAdmin := fields(t, g.do(t, http.MethodGet, path, "", admin))
	assert.Equal(t, []any{"shop", "form-daily"}, byAdmin["sources"])

	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodGet, path, "", bob).Code)
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodGet, path, "", "").Code)

	g.w.sourcesErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, path, "", jane).Code)
	g.w.sourcesErr = nil
	g.w.viewErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, path, "", jane).Code)
	g.w.viewErr = nil

	g.svc.Endpoints = nil
	noEndpoints := fields(t, g.do(t, http.MethodGet, path, "", jane))
	assert.NotContains(t, noEndpoints, "endpoint", "a deployment that cannot bind endpoints offers none")
}

// TestOneHandler is GET, PUT and DELETE /portal/handlers/{id} with enable and
// disable, each acting on the handler by its id for its script's owner or an
// administrator.
func TestOneHandler(t *testing.T) {
	g := newRig()
	id := g.add(t, "s1", `{"kind":"schedule","cron":"@daily","args":{}}`, jane)
	one := "/api/v1/portal/handlers/" + id

	got := fields(t, g.do(t, http.MethodGet, one, "", jane))
	assert.Equal(t, "@daily", got["cron"])
	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodGet, one, "", bob).Code, "another person's handler")
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodGet, "/api/v1/portal/handlers/nope", "", jane).Code)
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodGet, one, "", "").Code)

	changed := g.do(t, http.MethodPut, one, `{"cron":"@hourly"}`, jane)
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	assert.Equal(t, id, fields(t, changed)["handler"], "a change keeps the handler")
	assert.Equal(t, "@hourly", g.w.schedules[id].CronSpec)
	assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPut, one, `nope`, jane).Code)
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodPut, "/api/v1/portal/handlers/nope", `{}`, jane).Code)
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodPut, one, `{}`, "").Code)

	off := g.do(t, http.MethodPost, one+"/disable", "", jane)
	require.Equal(t, http.StatusOK, off.Code, off.Body.String())
	assert.Equal(t, false, fields(t, off)["enabled"])
	on := g.do(t, http.MethodPost, one+"/enable", "", jane)
	assert.Equal(t, true, fields(t, on)["enabled"])
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodPost, one+"/enable", "", "").Code)

	g.w.hasRuns[id] = true
	kept := g.do(t, http.MethodDelete, one, "", jane)
	assert.Equal(t, http.StatusForbidden, kept.Code, "a schedule its runs name is kept")
	assert.Contains(t, kept.Body.String(), "disable it with handler_disable")
	g.w.hasRuns[id] = false
	gone := g.do(t, http.MethodDelete, one, "", jane)
	require.Equal(t, http.StatusOK, gone.Code, gone.Body.String())
	assert.Equal(t, true, fields(t, gone)["deleted"])
	assert.Empty(t, g.w.schedules)
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodDelete, one, "", "").Code)
}

// TestChange_AWebhookKeepsItsSource: a change of a webhook handler that
// leaves event_source out keeps the source, and one naming another source
// is refused: a handler's source does not change.
func TestChange_AWebhookKeepsItsSource(t *testing.T) {
	g := newRig()
	id := g.add(t, "s1", `{"kind":"webhook","event_source":"form-daily"}`, jane)
	one := "/api/v1/portal/handlers/" + id
	g.do(t, http.MethodPost, one+"/disable", "", jane)

	rec := g.do(t, http.MethodPut, one, `{}`, jane)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "form-daily", fields(t, rec)["event_source"])

	rec = g.do(t, http.MethodPut, one, `{"event_source":"form-other"}`, jane)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "a handler's source does not change")

	rec = g.do(t, http.MethodPut, one, `{"kind":"schedule","cron":"@daily"}`, jane)
	require.Equal(t, http.StatusOK, rec.Code, "the kind is the handler's, whatever the body says: %s", rec.Body.String())
	assert.Len(t, g.w.webhooks, 1)
	assert.Empty(t, g.w.schedules)
}

// TestChange_AnEndpointByItsID: a change of an endpoint handler changes that
// binding, renamed or not.
func TestChange_AnEndpointByItsID(t *testing.T) {
	g := newRig()
	id := g.add(t, "s2", `{"kind":"endpoint","endpoint":"orders","auth_mode":"authenticated"}`, admin)
	g.add(t, "s2", `{"kind":"endpoint","endpoint":"orders-archive","auth_mode":"authenticated"}`, admin)
	rec := g.do(t, http.MethodPut, "/api/v1/portal/handlers/"+id, `{"endpoint":"orders-v2"}`, admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "orders-v2", g.w.bindings[id].Name)
	assert.Len(t, g.w.bindings, 2)
	assert.Equal(t, epbinding.AuthAuthenticated, g.w.bindings[id].AuthMode, "a field left out keeps its value")
}

// TestAdminList is GET /admin/handlers: every handler of every script,
// narrowed by type, search, owner and state, with the total.
func TestAdminList(t *testing.T) {
	g := newRig()
	g.w.scripts["s3"] = &script.Script{ID: "s3", Name: "theirs", OwnerEmail: bob, Source: mainSource, Enabled: true, Status: script.StatusActive}
	g.add(t, "s1", `{"kind":"schedule","cron":"@daily"}`, jane)
	off := g.add(t, "s3", `{"kind":"schedule","cron":"@hourly"}`, bob)
	g.do(t, http.MethodPost, "/api/v1/portal/handlers/"+off+"/disable", "", bob)
	g.add(t, "s2", `{"kind":"endpoint","endpoint":"orders","auth_mode":"authenticated"}`, admin)

	list := func(query string) (int, map[string]any) {
		rec := g.do(t, http.MethodGet, "/api/v1/admin/handlers"+query, "", admin)
		if rec.Code != http.StatusOK {
			return rec.Code, nil
		}
		return rec.Code, fields(t, rec)
	}
	code, all := list("")
	require.Equal(t, http.StatusOK, code)
	assert.InDelta(t, 3, all["total"], 0)
	assert.Len(t, all["data"], 3)

	_, schedules := list("?type=schedule")
	assert.InDelta(t, 2, schedules["total"], 0)
	_, both := list("?type=schedule&type=endpoint&owner=" + bob)
	assert.InDelta(t, 1, both["total"], 0)
	_, disabled := list("?enabled=false")
	assert.InDelta(t, 1, disabled["total"], 0)
	_, searched := list("?search=orders")
	assert.InDelta(t, 1, searched["total"], 0)

	_, _ = list("?limit=25&offset=50&search=%20orders%20")
	assert.Equal(t, 25, g.w.asked.Limit)
	assert.Equal(t, 50, g.w.asked.Offset)
	assert.Equal(t, "orders", g.w.asked.Search, "the search is trimmed")

	code, _ = list("?enabled=maybe")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = list("?type=rule")
	assert.Equal(t, http.StatusBadRequest, code, "a type no list filters on")
	g.w.viewErr = errors.New("db down")
	code, _ = list("")
	assert.Equal(t, http.StatusInternalServerError, code)
}
