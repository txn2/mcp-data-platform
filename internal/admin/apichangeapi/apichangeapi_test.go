package apichangeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/apichange"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

const spec = `openapi: 3.0.3
info: {title: Pets, version: "1"}
paths:
  /pets:
    get: {operationId: listPets, responses: {"200": {description: ok}}}
  /pets/{id}:
    delete:
      operationId: deletePet
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: gone}}
`

type fakeService struct {
	res      apichange.Result
	err      error
	trigger  string
	by       string
	discards int
}

func (f *fakeService) Check(_ context.Context, _ apichange.SpecKey, trigger, by string) (apichange.Result, error) {
	f.trigger, f.by = trigger, by
	return f.res, f.err
}

func (f *fakeService) Apply(_ context.Context, _ apichange.SpecKey, _, by string) (apichange.Result, error) {
	f.by = by
	return f.res, f.err
}

func (f *fakeService) Discard(context.Context, apichange.SpecKey, string, string) error {
	f.discards++
	return f.err
}

type fakeStore struct {
	watch   apichange.Watch
	saved   *apichange.Watch
	changes []apichange.Change
	err     error
}

func (f *fakeStore) GetWatch(context.Context, apichange.SpecKey) (apichange.Watch, error) {
	return f.watch, f.err
}

func (f *fakeStore) SaveSettings(_ context.Context, w apichange.Watch, _ string) error {
	f.saved = &w
	f.watch = w
	return f.err
}

func (f *fakeStore) ListChanges(context.Context, apichange.SpecKey, int) ([]apichange.Change, error) {
	return f.changes, f.err
}

func (f *fakeStore) GetChange(_ context.Context, _ apichange.SpecKey, id string) (apichange.Change, error) {
	for _, c := range f.changes {
		if c.ID == id {
			return c, nil
		}
	}
	return apichange.Change{}, apichange.ErrNoChange
}

type fakeCatalogs struct {
	specs map[string]apicatalog.SpecEntry
	err   error
}

func (f *fakeCatalogs) GetSpec(_ context.Context, _, name string) (*apicatalog.SpecEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.specs[name]
	if !ok {
		return nil, apicatalog.ErrNotFound
	}
	return &s, nil
}

func (f *fakeCatalogs) ListSpecs(context.Context, string) ([]apicatalog.SpecEntry, error) {
	out := make([]apicatalog.SpecEntry, 0, len(f.specs))
	for _, s := range f.specs {
		out = append(out, s)
	}
	return out, f.err
}

func (*fakeCatalogs) ReferencingConnections(context.Context, string) ([]apicatalog.ConnectionRef, error) {
	return []apicatalog.ConnectionRef{{Kind: "api", Name: "vendor"}}, nil
}

type rig struct {
	mux      *http.ServeMux
	svc      *fakeService
	store    *fakeStore
	cats     *fakeCatalogs
	settings apichange.Settings
}

func newRig(mutable bool) *rig {
	g := &rig{
		svc:   &fakeService{},
		store: &fakeStore{},
		cats: &fakeCatalogs{specs: map[string]apicatalog.SpecEntry{
			"pets":   {SpecName: "pets", Content: spec, SourceKind: apicatalog.SourceURL},
			"upload": {SpecName: "upload", Content: spec, SourceKind: apicatalog.SourceUpload},
		}},
		settings: apichange.Settings{Channels: []string{}},
	}
	g.mux = http.NewServeMux()
	Register(g.mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: g.svc, Store: g.store, Catalogs: g.cats, Mutable: mutable,
		Author: func(*http.Request) string { return "admin@example.com" },
		Callers: func(_ context.Context, conns []string, idx apichange.OperationIndex) (map[string][]apichange.Caller, error) {
			if len(idx.Ops()) == 0 {
				return nil, errors.New("no ops")
			}
			return map[string][]apichange.Caller{"listPets": {{Kind: "script", ID: "s1", Name: "nightly", Via: "source", Connection: conns[0]}}}, nil
		},
		CatalogOf: func(_ context.Context, conn string) string {
			if conn == "vendor" {
				return "acme"
			}
			return ""
		},
		Warnings: func(context.Context, string) ([]apichange.Warning, error) {
			return []apichange.Warning{{Connection: "vendor", Operation: "listPets", Kind: "sunset"}}, nil
		},
		ReadSettings: func(context.Context) (apichange.Settings, error) { return g.settings, nil },
		SaveSettings: func(_ context.Context, s apichange.Settings, _ string) error {
			g.settings = s
			return nil
		},
		ChannelExists: func(_ context.Context, name string) bool { return name == "ops" },
	})
	return g
}

func (g *rig) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, &buf))
	return rec
}

const base = "/api/v1/admin/api-catalogs/acme/specs/pets"

func TestRefresh(t *testing.T) {
	g := newRig(true)
	fetched := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	g.svc.res = apichange.Result{
		Spec:   &apicatalog.SpecEntry{SpecName: "pets", SourceKind: "url", ETag: `"v2"`, OperationCount: 2, LastFetchedAt: fetched},
		Change: &apichange.Change{ID: "c1", Status: apichange.StatusApplied},
	}
	rec := g.do(t, http.MethodPost, base+"/refresh", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var view RefreshView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Equal(t, "c1", view.Change.ID)
	assert.Equal(t, "2026-11-05T00:00:00Z", view.LastFetchedAt)
	assert.Equal(t, apichange.TriggerManual, g.svc.trigger)
	assert.Equal(t, "admin@example.com", g.svc.by)

	for err, code := range map[error]int{
		apicatalog.ErrNotFound:                                       http.StatusNotFound,
		apichange.ErrNotRefreshable:                                  http.StatusBadRequest,
		fmt.Errorf("x: %w", apicatalog.ErrSSRFBlocked):               http.StatusBadRequest,
		fmt.Errorf("x: %w", apicatalog.ErrTooLarge):                  http.StatusRequestEntityTooLarge,
		fmt.Errorf("x: %w", apicatalog.ErrUpstream):                  http.StatusServiceUnavailable,
		errors.New("the fetched spec is not valid: bad"):             http.StatusBadRequest,
		errors.New("saving the refreshed spec: pq: connection lost"): http.StatusInternalServerError,
	} {
		g.svc.err = err
		rec := g.do(t, http.MethodPost, base+"/refresh", nil)
		assert.Equal(t, code, rec.Code, err.Error())
	}
	g.svc.err = errors.New("saving the refreshed spec: pq: connection lost")
	assert.NotContains(t, g.do(t, http.MethodPost, base+"/refresh", nil).Body.String(), "pq:",
		"an internal failure's detail stays in the log")
}

func TestWatch(t *testing.T) {
	g := newRig(true)
	next := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	g.store.watch = apichange.Watch{RefreshInterval: 168 * time.Hour, HoldChanges: true, NextCheckAt: &next}
	g.store.changes = []apichange.Change{{ID: "c0", Status: apichange.StatusApplied}, {ID: "c1", Status: apichange.StatusPending}}
	rec := g.do(t, http.MethodGet, base+"/watch", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var view WatchView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Equal(t, WatchView{RefreshInterval: "168h0m0s", HoldChanges: true, NextCheckAt: "2026-11-12T00:00:00Z", PendingChange: "c1"}, view)

	rec = g.do(t, http.MethodPut, base+"/watch", WatchInput{RefreshInterval: "24h", HoldChanges: true})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 24*time.Hour, g.store.saved.RefreshInterval)
	assert.Equal(t, apichange.SpecKey{CatalogID: "acme", SpecName: "pets"}, g.store.saved.SpecKey)

	upload := "/api/v1/admin/api-catalogs/acme/specs/upload/watch"
	rec = g.do(t, http.MethodPut, upload, WatchInput{RefreshInterval: "168h", UpstreamURL: " https://vendor.example/openapi.yaml "})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "https://vendor.example/openapi.yaml", g.store.saved.UpstreamURL)

	for name, tc := range map[string]struct {
		path string
		in   any
	}{
		"too often":                {base + "/watch", WatchInput{RefreshInterval: "5m"}},
		"not a duration":           {base + "/watch", WatchInput{RefreshInterval: "weekly"}},
		"upstream on a url spec":   {base + "/watch", WatchInput{UpstreamURL: "https://x.example/a"}},
		"hold on an upload":        {upload, WatchInput{HoldChanges: true}},
		"schedule with no source":  {upload, WatchInput{RefreshInterval: "24h"}},
		"not a url":                {upload, WatchInput{UpstreamURL: "file:///etc/passwd"}},
		"an unknown field":         {base + "/watch", map[string]any{"refresh_interval": "", "nope": 1}},
		"a body that is not json:": {base + "/watch", "["},
	} {
		rec := g.do(t, http.MethodPut, tc.path, tc.in)
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodPut, "/api/v1/admin/api-catalogs/acme/specs/ghost/watch", WatchInput{}).Code)
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodGet, "/api/v1/admin/api-catalogs/acme/specs/ghost/watch", nil).Code)

	g.store.err = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, base+"/watch", nil).Code)
}

func TestValidateWatch_EmbeddedAndGraphQL(t *testing.T) {
	_, err := validateWatch(WatchInput{}, &apicatalog.SpecEntry{SourceKind: apicatalog.SourceEmbedded})
	require.Error(t, err)
	_, err = validateWatch(WatchInput{UpstreamURL: "https://x.example/s.graphql"},
		&apicatalog.SpecEntry{SourceKind: apicatalog.SourceInline, SpecFormat: apicatalog.FormatGraphQL})
	require.ErrorIs(t, err, apichange.ErrNotOpenAPI)
	require.Error(t, checkURL("https://"+string(bytes.Repeat([]byte("a"), 2100))+".example/"))
}

func TestChangesAndDecisions(t *testing.T) {
	g := newRig(true)
	g.store.changes = []apichange.Change{{ID: "c1", Status: apichange.StatusPending, Summary: "1 added"}}
	rec := g.do(t, http.MethodGet, base+"/changes", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"summary":"1 added"`)
	assert.NotContains(t, rec.Body.String(), "pending_content", "a held document is never served")

	assert.Equal(t, http.StatusOK, g.do(t, http.MethodGet, base+"/changes/c1", nil).Code)
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodGet, base+"/changes/nope", nil).Code)

	g.svc.res = apichange.Result{Spec: &apicatalog.SpecEntry{SpecName: "pets"}, Change: &apichange.Change{ID: "c1", Status: apichange.StatusApplied}}
	assert.Equal(t, http.StatusOK, g.do(t, http.MethodPost, base+"/changes/c1/apply", nil).Code)
	assert.Equal(t, http.StatusOK, g.do(t, http.MethodPost, base+"/changes/c1/discard", nil).Code)

	g.svc.err = apichange.ErrNotPending
	assert.Equal(t, http.StatusConflict, g.do(t, http.MethodPost, base+"/changes/c1/apply", nil).Code)
	assert.Equal(t, http.StatusConflict, g.do(t, http.MethodPost, base+"/changes/c1/discard", nil).Code)
	g.svc.err = errors.New("the held spec is not valid: x")
	assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPost, base+"/changes/c1/apply", nil).Code)
	g.svc.err = apichange.ErrNoChange
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodPost, base+"/changes/c1/apply", nil).Code)

	g.store.err = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, base+"/changes", nil).Code)
}

func TestUsedBy(t *testing.T) {
	g := newRig(true)
	rec := g.do(t, http.MethodGet, base+"/operations/listPets/used-by", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var op UsedByView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &op))
	require.Len(t, op.UsedBy, 1)
	assert.Equal(t, "vendor", op.UsedBy[0].Connection)

	rec = g.do(t, http.MethodGet, base+"/operations/deletePet/used-by", nil)
	assert.JSONEq(t, `{"operation":"deletePet","used_by":[]}`, rec.Body.String(), "nothing uses it: [] never null")
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodGet, "/api/v1/admin/api-catalogs/acme/specs/ghost/operations/x/used-by", nil).Code)

	rec = g.do(t, http.MethodGet, "/api/v1/admin/connection-instances/api/vendor/used-by", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var conn ConnectionUsedByView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conn))
	assert.Equal(t, "acme", conn.CatalogID)
	require.Len(t, conn.Operations, 1)
	assert.Equal(t, "listPets", conn.Operations[0].Operation)

	rec = g.do(t, http.MethodGet, "/api/v1/admin/connection-instances/api/nocatalog/used-by", nil)
	assert.JSONEq(t, `{"connection":"nocatalog","operations":[]}`, rec.Body.String())

	g.cats.err = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, "/api/v1/admin/connection-instances/api/vendor/used-by", nil).Code)
}

func TestWarningsAndSettings(t *testing.T) {
	g := newRig(true)
	rec := g.do(t, http.MethodGet, "/api/v1/admin/connection-instances/api/vendor/warnings", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"kind":"sunset"`)

	assert.JSONEq(t, `{"channels":[]}`, g.do(t, http.MethodGet, "/api/v1/admin/settings/api-changes", nil).Body.String())
	rec = g.do(t, http.MethodPut, "/api/v1/admin/settings/api-changes", apichange.Settings{Channels: []string{"ops"}})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"ops"}, g.settings.Channels)
	rec = g.do(t, http.MethodPut, "/api/v1/admin/settings/api-changes", apichange.Settings{Channels: []string{"nope"}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = g.do(t, http.MethodPut, "/api/v1/admin/settings/api-changes", map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{}, g.settings.Channels)
	assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPut, "/api/v1/admin/settings/api-changes", "x").Code)
}

func TestFailuresKeepTheirDetailInTheLog(t *testing.T) {
	mux := http.NewServeMux()
	down := errors.New("pq: db down")
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &fakeService{}, Store: &fakeStore{}, Catalogs: &fakeCatalogs{}, Mutable: true,
		Warnings:     func(context.Context, string) ([]apichange.Warning, error) { return nil, down },
		ReadSettings: func(context.Context) (apichange.Settings, error) { return apichange.Settings{}, down },
		SaveSettings: func(context.Context, apichange.Settings, string) error { return down },
	})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/admin/connection-instances/api/vendor/warnings", ""},
		{http.MethodGet, "/api/v1/admin/settings/api-changes", ""},
		{http.MethodPut, "/api/v1/admin/settings/api-changes", `{"channels":[]}`},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, bytes.NewBufferString(tc.body)))
		assert.Equal(t, http.StatusInternalServerError, rec.Code, tc.path)
		assert.NotContains(t, rec.Body.String(), "pq:", tc.path)
	}
}

func TestRegister_ReadOnlyAndNoDatabase(t *testing.T) {
	g := newRig(false)
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodPost, base+"/refresh", nil).Code, "file config mode registers reads only")
	assert.Equal(t, http.StatusOK, g.do(t, http.MethodGet, base+"/watch", nil).Code)

	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{Service: &fakeService{}, Catalogs: &fakeCatalogs{}, Mutable: true})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"/watch", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "no database: no watch routes")

	Register(http.NewServeMux(), nil, Config{}) // nothing to serve, nothing registered
}
