package apptool

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
)

// envs is an in-memory application.EnvironmentStore over a store's apps.
type envs struct {
	s    *store
	vars []application.Variable
}

func (e *envs) app(id string) *application.Application {
	for _, a := range e.s.apps {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (e *envs) AddEnvironment(_ context.Context, id string, env application.Environment, after string) error {
	a := e.app(id)
	i := -1
	if after != "" {
		_, i, _ = a.EnvironmentNamed(after)
	}
	a.Environments = slices.Insert(a.Environments, i+1, env)
	return nil
}

func (e *envs) RemoveEnvironment(_ context.Context, id, name string) error {
	a := e.app(id)
	a.Environments = slices.DeleteFunc(a.Environments, func(x application.Environment) bool { return x.Name == name })
	return nil
}

func (e *envs) SetEnvironmentRelease(_ context.Context, id string, m application.Move) error {
	env, _, _ := e.app(id).EnvironmentNamed(m.Environment)
	env.Release = m.Number
	return nil
}

func (e *envs) SetEnvironmentApproval(_ context.Context, id, name string, requires bool, _ string) error {
	env, _, _ := e.app(id).EnvironmentNamed(name)
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

// Environments and variables through manage_application (#2696): an agent
// promotes, but not into an environment that requires approval.
func TestTool_EnvironmentsAndVariables(t *testing.T) {
	st := &store{apps: map[string]*application.Application{}, releases: []application.Release{{Number: 1}}}
	ev := &envs{s: st}
	tool := &Tool{Service: &appsvc.Service{Store: st, Scripts: scripts{}, Environments: ev}}
	owner := session(t, tool, pcFor("jane@example.com", false))
	adm := session(t, tool, pcFor("admin@example.com", true))

	text, isErr := call(t, owner, map[string]any{"command": "create", "name": "shop"})
	require.False(t, isErr, text)
	st.apps["shop"].Environments = []application.Environment{{Name: "prod"}}
	text, isErr = call(t, adm, map[string]any{"command": "add_environment", "application": "shop", "environment": "dev"})
	require.False(t, isErr, text)
	st.apps["shop"].Environments[0].Release = 1

	text, isErr = call(t, adm, map[string]any{"command": "add_environment", "application": "shop", "environment": "stage", "after": "dev", "requires_approval": true})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"stage"`)
	text, isErr = call(t, adm, map[string]any{"command": "promote", "application": "shop", "environment": "dev"})
	require.True(t, isErr)
	assert.Contains(t, text, "APPROVAL_REQUIRED")
	text, isErr = call(t, adm, map[string]any{"command": "rollback", "application": "shop", "environment": "stage"})
	require.True(t, isErr)
	assert.Contains(t, text, "APPROVAL_REQUIRED")

	text, isErr = call(t, adm, map[string]any{"command": "set_environment", "application": "shop", "environment": "stage"})
	require.True(t, isErr)
	assert.Contains(t, text, "requires_approval")
	text, isErr = call(t, adm, map[string]any{"command": "set_environment", "application": "shop", "environment": "stage", "requires_approval": false})
	require.False(t, isErr, text)
	text, isErr = call(t, adm, map[string]any{"command": "promote", "application": "shop", "environment": "dev"})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"to":"stage"`)
	text, isErr = call(t, adm, map[string]any{"command": "promote", "application": "shop", "environment": "prod"})
	require.True(t, isErr)
	assert.Contains(t, text, "last environment")
	text, isErr = call(t, adm, map[string]any{"command": "remove_environment", "application": "shop", "environment": "stage"})
	require.False(t, isErr, text)

	text, isErr = call(t, owner, map[string]any{"command": "set_variable", "application": "shop", "variable": "API_BASE", "value": "https://x.example.com", "environment": "prod"})
	require.False(t, isErr, text)
	text, isErr = call(t, owner, map[string]any{"command": "variables", "application": "shop"})
	require.False(t, isErr, text)
	assert.Contains(t, text, "https://x.example.com")
	text, isErr = call(t, owner, map[string]any{"command": "delete_variable", "application": "shop", "variable": "API_BASE", "environment": "prod"})
	require.False(t, isErr, text)
	assert.Empty(t, ev.vars)
	text, isErr = call(t, adm, map[string]any{"command": "promote", "application": "nope", "environment": "dev"})
	require.True(t, isErr)
	assert.Contains(t, text, "no such application")
	_, isErr = call(t, owner, map[string]any{"command": "variables", "application": "nope"})
	require.True(t, isErr)
	_, isErr = call(t, owner, map[string]any{"command": "set_variable", "application": "shop", "variable": "bad", "value": "v"})
	require.True(t, isErr)
	_, isErr = call(t, owner, map[string]any{"command": "delete_variable", "application": "nope", "variable": "A"})
	require.True(t, isErr)
}
