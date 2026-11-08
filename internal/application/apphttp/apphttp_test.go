package apphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appbundle"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type store struct {
	apps     map[string]*application.Application
	releases []application.Release
}

func (s *store) Create(_ context.Context, a *application.Application) error {
	if s.apps[a.Name] != nil {
		return application.ErrNameTaken
	}
	a.ID = "app_" + a.Name
	s.apps[a.Name] = a
	return nil
}

func (s *store) find(id string) *application.Application {
	for _, a := range s.apps {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (s *store) Get(_ context.Context, id string) (*application.Application, error) {
	if a := s.find(id); a != nil {
		cp := *a
		return &cp, nil
	}
	return nil, application.ErrNotFound
}

func (s *store) GetByName(_ context.Context, name string) (*application.Application, error) {
	if a := s.apps[name]; a != nil {
		cp := *a
		return &cp, nil
	}
	return nil, application.ErrNotFound
}

func (s *store) List(context.Context) ([]application.Application, error) {
	out := make([]application.Application, 0, len(s.apps))
	for _, a := range s.apps {
		out = append(out, *a)
	}
	return out, nil
}

func (s *store) Update(_ context.Context, a *application.Application) error {
	s.find(a.ID).Description = a.Description
	return nil
}

func (s *store) Delete(_ context.Context, id string) error {
	delete(s.apps, s.find(id).Name)
	return nil
}

func (s *store) AddPart(_ context.Context, id string, p application.Part) error {
	s.find(id).Parts = append(s.find(id).Parts, p)
	return nil
}

func (s *store) RemovePart(_ context.Context, id, _, _ string) error {
	a := s.find(id)
	if len(a.Parts) == 0 {
		return application.ErrNoPart
	}
	a.Parts = a.Parts[1:]
	return nil
}

func (*store) Owning(context.Context, string, string) (*application.Application, error) {
	return nil, application.ErrNotFound
}

func (s *store) SetSchema(_ context.Context, id string, h application.HeldSchema) error {
	a := s.find(id)
	a.Connection, a.Schema = h.Connection, h.Schema
	a.Parts = append(a.Parts, application.Part{Kind: application.KindSchema, ID: application.SchemaPartID(h.Connection, h.Schema)})
	return nil
}

func (*store) SetRunRole(context.Context, string, application.HeldSchema) error { return nil }

func (s *store) ClearSchema(_ context.Context, id string) error {
	a := s.find(id)
	a.Connection, a.Schema = "", ""
	return nil
}

func (s *store) Holding(_ context.Context, connection, schema string) (*application.Application, error) {
	for _, a := range s.apps {
		if a.Connection == connection && a.Schema == schema {
			cp := *a
			return &cp, nil
		}
	}
	return nil, application.ErrNotFound
}

func (*store) RunRoleOf(context.Context, string) (*application.RunRole, error) {
	return nil, application.ErrNoRunRole
}

func (s *store) Releases(context.Context, string) ([]application.Release, error) {
	return s.releases, nil
}

func (s *store) Release(_ context.Context, _ string, n int) (*application.Release, error) {
	for _, r := range s.releases {
		if r.Number == n {
			return &r, nil
		}
	}
	return nil, application.ErrNoRelease
}

func (s *store) Record(_ context.Context, id string, r application.Recorded) (*application.Release, error) {
	rel := r.Release
	rel.Number = len(s.releases) + 1
	s.releases = append(s.releases, rel)
	s.find(id).CurrentRelease = rel.Number
	return &rel, nil
}

type scripts struct{}

func (scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return &script.Script{ID: id, OwnerEmail: "jane@example.com", Version: 1}, nil
}

func (scripts) GetVersion(context.Context, string, int) (*script.Version, error) {
	return &script.Version{}, nil
}

type bundles struct{ imported *appbundle.Bundle }

func (*bundles) Bundle(_ context.Context, actor application.Actor, ref string, _ int) (*appbundle.Bundle, error) {
	if !actor.Admin {
		return nil, application.ErrNotAllowed
	}
	return &appbundle.Bundle{Format: appbundle.Format, Application: appbundle.AppInfo{Name: ref}, Release: 1}, nil
}

func (b *bundles) ImportBundle(_ context.Context, _ application.Actor, bundle *appbundle.Bundle, _ map[string]string) (*appbundle.Imported, error) {
	b.imported = bundle
	if bundle == nil {
		return nil, &appsvc.RefusedError{Msg: "no bundle"}
	}
	return &appbundle.Imported{Application: bundle.Application.Name}, nil
}

type auditor struct{ events []middleware.AuditEvent }

func (a *auditor) Log(_ context.Context, ev middleware.AuditEvent) error {
	a.events = append(a.events, ev)
	return nil
}

type server struct {
	mux   *http.ServeMux
	who   *application.Actor
	audit *auditor
	b     *bundles
}

func newServer() *server {
	sv := &server{mux: http.NewServeMux(), audit: &auditor{}, b: &bundles{}}
	Register(sv.mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &appsvc.Service{Store: &store{apps: map[string]*application.Application{}}, Scripts: scripts{}},
		Bundles: sv.b, Actor: func(*http.Request) *application.Actor { return sv.who }, Audit: sv.audit,
	})
	return sv
}

func (sv *server) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	} else {
		r = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	sv.mux.ServeHTTP(w, r)
	return w
}

func TestRoutes(t *testing.T) {
	sv := newServer()
	assert.Equal(t, http.StatusUnauthorized, sv.do(t, "GET", Prefix, "").Code)

	sv.who = &application.Actor{Email: "jane@example.com"}
	w := sv.do(t, "POST", Prefix, `{"name":"shop","description":"d"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusConflict, sv.do(t, "POST", Prefix, `{"name":"shop"}`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "POST", Prefix, `{"name":"Bad"}`).Code)
	assert.Equal(t, http.StatusBadRequest, sv.do(t, "POST", Prefix, `{`).Code)

	w = sv.do(t, "GET", Prefix, "")
	require.Equal(t, http.StatusOK, w.Code)
	var list listResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Equal(t, 1, list.Total)

	assert.Equal(t, http.StatusOK, sv.do(t, "PATCH", Prefix+"/shop", `{"description":"new"}`).Code)
	w = sv.do(t, "POST", Prefix+"/shop/parts", `{"kind":"script","id":"11111111-1111-1111-1111-111111111111"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusForbidden, sv.do(t, "POST", Prefix+"/shop/release", `{}`).Code, "an owner does not release")
	assert.Equal(t, http.StatusForbidden, sv.do(t, "GET", Prefix+"/shop/export", "").Code)

	sv.who = &application.Actor{Email: "admin@example.com", Admin: true}
	w = sv.do(t, "POST", Prefix+"/shop/release", `{"note":"first"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = sv.do(t, "GET", Prefix+"/shop", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"releases"`)
	assert.Contains(t, w.Body.String(), `"current_release"`)
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "POST", Prefix+"/shop/rollback", `{}`).Code)
	assert.Equal(t, http.StatusNotFound, sv.do(t, "GET", Prefix+"/none", "").Code)

	w = sv.do(t, "GET", Prefix+"/shop/export", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "shop-release-1.json")

	w = sv.do(t, "POST", Prefix+"/import", `{"bundle":{"format":"x","application":{"name":"shop"},"release":1},"map":{"orders":"orders"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "POST", Prefix+"/import", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, sv.do(t, "POST", Prefix+"/import", `nope`).Code)

	assert.Equal(t, http.StatusOK, sv.do(t, "DELETE", Prefix+"/shop/parts/script/11111111-1111-1111-1111-111111111111", "").Code)
	assert.Equal(t, http.StatusNotFound, sv.do(t, "DELETE", Prefix+"/shop/parts/script/x", "").Code)
	assert.Equal(t, http.StatusOK, sv.do(t, "DELETE", Prefix+"/shop", "").Code)

	var imported *middleware.AuditEvent
	for i, ev := range sv.audit.events {
		assert.Equal(t, "manage_application", ev.ToolName)
		assert.Equal(t, "portal", ev.Source)
		if ev.Parameters["command"] == "import" && ev.Success {
			imported = &sv.audit.events[i]
		}
	}
	require.NotNil(t, imported)
	_, carriesBundle := imported.Parameters["bundle"]
	assert.False(t, carriesBundle, "an import's audit row never holds the bundle")
	assert.Equal(t, "shop", imported.Parameters["application"])
}

func TestWithoutBundlesAndStatus(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &appsvc.Service{Store: &store{apps: map[string]*application.Application{}}},
		Actor:   func(*http.Request) *application.Actor { return &application.Actor{Email: "a@b", Admin: true} },
	})
	for _, c := range []struct{ method, path, body string }{
		{"GET", Prefix + "/shop/export", ""}, {"POST", Prefix + "/import", `{"bundle":null}`},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader(c.body)))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, c.path)
	}
	assert.Equal(t, http.StatusInternalServerError, status(errors.New("boom")))
	assert.Equal(t, http.StatusConflict, status(application.ErrStale))
	assert.NotPanics(t, func() { Register(http.NewServeMux(), nil, Config{}) })
}

// TestDatabasesRoute is the route the New application dialog fills its
// choices from (#2491): signed in only, never null, and absent without a
// source.
func TestDatabasesRoute(t *testing.T) {
	mux := http.NewServeMux()
	var who *application.Actor
	var asked application.Actor
	answer := []DatabaseChoice{{Connection: "orders", Schemas: []SchemaChoice{{Name: "shop", HeldBy: "shop"}, {Name: "public"}}}}
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &appsvc.Service{Store: &store{apps: map[string]*application.Application{}}},
		Actor:   func(*http.Request) *application.Actor { return who },
		Databases: func(_ *http.Request, a application.Actor) []DatabaseChoice {
			asked = a
			if a.Email == "none@example.com" {
				return nil
			}
			return answer
		},
	})
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", DatabasesPath, http.NoBody))
		return w
	}
	assert.Equal(t, http.StatusUnauthorized, get().Code)
	who = &application.Actor{Email: "jane@example.com", Roles: []string{"analyst"}}
	w := get()
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"connections":[{"connection":"orders","schemas":[{"name":"shop","held_by":"shop"},{"name":"public"}]}]}`, w.Body.String())
	assert.Equal(t, []string{"analyst"}, asked.Roles)
	who = &application.Actor{Email: "none@example.com"}
	assert.JSONEq(t, `{"connections":[]}`, get().Body.String())

	bare := http.NewServeMux()
	Register(bare, func(h http.Handler) http.Handler { return h }, Config{
		Service: &appsvc.Service{Store: &store{apps: map[string]*application.Application{}}},
		Actor:   func(*http.Request) *application.Actor { return who },
	})
	w = httptest.NewRecorder()
	bare.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", DatabasesPath, http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestPartViewsAndCandidates is #2527's route half: the page reads each part
// by name, and the Add parts dialog searches what may be added, with kind
// checked.
func TestPartViewsAndCandidates(t *testing.T) {
	sv := newServer()
	sv.who = &application.Actor{Email: "jane@example.com"}
	require.Equal(t, http.StatusOK, sv.do(t, "POST", Prefix, `{"name":"shop"}`).Code)
	require.Equal(t, http.StatusOK, sv.do(t, "POST", Prefix+"/shop/parts", `{"kind":"script","id":"11111111-1111-1111-1111-111111111111"}`).Code)

	w := sv.do(t, "GET", Prefix+"/shop", "")
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Parts []appsvc.PartView `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Parts, 1)
	assert.Equal(t, appsvc.PartOK, got.Parts[0].State)
	require.NotNil(t, got.Parts[0].LatestVersion)
	assert.Equal(t, 1, *got.Parts[0].LatestVersion)

	w = sv.do(t, "GET", Prefix+"/shop/candidates?q=x", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"candidates":[],"counts":{}}`, w.Body.String())
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "GET", Prefix+"/shop/candidates?kind=table", "").Code)
	sv.who = &application.Actor{Email: "joe@example.com"}
	assert.Equal(t, http.StatusForbidden, sv.do(t, "GET", Prefix+"/shop/candidates", "").Code)
	sv.who = nil
	assert.Equal(t, http.StatusUnauthorized, sv.do(t, "GET", Prefix+"/shop/candidates", "").Code)
}
