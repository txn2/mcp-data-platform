package apphttp

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
)

// envs is an in-memory application.EnvironmentStore over a store.
type envs struct {
	s    *store
	vars []application.Variable
}

func (e *envs) AddEnvironment(_ context.Context, id string, env application.Environment, after string) error {
	a := e.s.find(id)
	i := -1
	if after != "" {
		_, i, _ = a.EnvironmentNamed(after)
	}
	a.Environments = slices.Insert(a.Environments, i+1, env)
	return nil
}

func (e *envs) RemoveEnvironment(_ context.Context, id, name string) error {
	a := e.s.find(id)
	a.Environments = slices.DeleteFunc(a.Environments, func(x application.Environment) bool { return x.Name == name })
	return nil
}

func (e *envs) SetEnvironmentRelease(_ context.Context, id string, m application.Move) error {
	env, _, _ := e.s.find(id).EnvironmentNamed(m.Environment)
	env.Release = m.Number
	return nil
}

func (e *envs) SetEnvironmentApproval(_ context.Context, id, name string, requires bool, _ string) error {
	env, _, _ := e.s.find(id).EnvironmentNamed(name)
	env.RequiresApproval = requires
	return nil
}

func (e *envs) Variables(context.Context, string) ([]application.Variable, error) { return e.vars, nil }

func (e *envs) SetVariable(_ context.Context, _ string, v application.Variable) error {
	e.vars = append(e.vars, v)
	return nil
}

func (e *envs) DeleteVariable(_ context.Context, _, env, name string) error {
	e.vars = slices.DeleteFunc(e.vars, func(v application.Variable) bool { return v.Name == name && v.Environment == env })
	return nil
}

// The portal's environment and variable routes (#2696). A variable's value is
// never in the audit row.
func TestEnvironmentRoutes(t *testing.T) {
	st := &store{apps: map[string]*application.Application{}}
	ev := &envs{s: st}
	sv := &server{mux: http.NewServeMux(), audit: &auditor{}, b: &bundles{}}
	Register(sv.mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &appsvc.Service{Store: st, Scripts: scripts{}, Environments: ev},
		Bundles: sv.b, Actor: func(*http.Request) *application.Actor { return sv.who }, Audit: sv.audit,
	})
	sv.who = &application.Actor{Email: "jane@example.com"}
	w := sv.do(t, "POST", Prefix, `{"name":"shop","environments":["dev","prod"]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"dev"`)

	w = sv.do(t, "PUT", Prefix+"/shop/variables/API_KEY", `{"environment":"prod","value":"{{secret:live-key}}"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "{{secret:live-key}}")
	for _, ev := range sv.audit.events {
		assert.NotContains(t, ev.Parameters, "value")
		for _, v := range ev.Parameters {
			assert.NotContains(t, v, "live-key", "a variable's value is not recorded")
		}
	}
	w = sv.do(t, "GET", Prefix+"/shop/variables", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "API_KEY")
	w = sv.do(t, "DELETE", Prefix+"/shop/variables/API_KEY?environment=prod", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, ev.vars)
	assert.Equal(t, http.StatusForbidden, sv.do(t, "POST", Prefix+"/shop/environments", `{"name":"stage","after":"dev"}`).Code)

	sv.who = &application.Actor{Email: "admin@example.com", Admin: true}
	w = sv.do(t, "POST", Prefix+"/shop/environments", `{"name":"stage","after":"dev","requires_approval":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = sv.do(t, "PATCH", Prefix+"/shop/environments/stage", `{"requires_approval":false}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	st.find("app_shop").Environments[0].Release = 1
	st.releases = []application.Release{{Number: 1}}
	w = sv.do(t, "POST", Prefix+"/shop/environments/dev/promote", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"to":"stage"`)
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "POST", Prefix+"/shop/environments/stage/rollback", `{}`).Code)
	w = sv.do(t, "DELETE", Prefix+"/shop/environments/stage", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.False(t, strings.Contains(w.Body.String(), `"stage"`))
	assert.Equal(t, http.StatusBadRequest, sv.do(t, "PUT", Prefix+"/shop/variables/A", `{`).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "PUT", Prefix+"/shop/variables/lower", `{"value":"v"}`).Code)
}
