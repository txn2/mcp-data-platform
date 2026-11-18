package eprun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appenv"
)

// envResolver answers each environment of one application.
type envResolver struct {
	envs map[string]appenv.Script
	err  error
}

func (e envResolver) ForScript(_ context.Context, _, env string) (*appenv.Script, error) {
	if e.err != nil {
		return nil, e.err
	}
	if env == "" {
		env = "prod"
	}
	s, ok := e.envs[env]
	if !ok {
		return nil, appenv.ErrNoEnvironment
	}
	return &s, nil
}

const envSource = `
def handle(req):
    """Says where it runs."""
    return {"status": 200, "json": {"env": platform.environment(), "base": platform.env("API_BASE"), "v": "%s"}}
`

// An endpoint answers in each environment with that environment's pinned
// version and variables (#2696).
func TestResolveIn_RunsTheEnvironmentsVersionAndVariables(t *testing.T) {
	f := newFixture(t, envSource[:len(envSource)-1]+"\n")
	f.scripts.save(envSource)
	st := f.bindings.stamps["orders"]
	st.Application = "app_1"
	f.bindings.stamps["orders"] = st
	f.runner.Environments = envResolver{envs: map[string]appenv.Script{
		"prod": {OK: true, Environment: "prod", Version: 2, Variables: map[string]string{"API_BASE": "https://pay.example.com"}},
		"dev":  {OK: true, Environment: "dev", Version: 1, Variables: map[string]string{"API_BASE": "https://sandbox.example.com"}},
		"qa":   {OK: true, Environment: "qa"},
	}}
	ctx := context.Background()
	get := Request{Method: "GET", Path: "/x", Params: map[string]string{"id": "1"}}

	prod, err := f.runner.ResolveIn(ctx, "orders", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", prod.Environment)
	body := decode(t, f.runner.Serve(ctx, prod, get).Body)
	assert.Equal(t, map[string]any{"env": "prod", "base": "https://pay.example.com", "v": "%s"}, body)

	dev, err := f.runner.ResolveIn(ctx, "orders", "dev")
	require.NoError(t, err)
	assert.Equal(t, 1, dev.Version.Version)
	body = decode(t, f.runner.Serve(ctx, dev, get).Body)
	assert.Equal(t, "dev", body["env"])
	assert.Equal(t, "https://sandbox.example.com", body["base"])
	again, err := f.runner.ResolveIn(ctx, "orders", "dev")
	require.NoError(t, err)
	assert.Same(t, dev.program, again.program, "the environment's compiled module is kept while its version holds")

	_, err = f.runner.ResolveIn(ctx, "orders", "qa")
	require.ErrorIs(t, err, ErrNoEnvironment, "an environment running no release answers nothing")
	_, err = f.runner.ResolveIn(ctx, "orders", "stage")
	require.ErrorIs(t, err, ErrNoEnvironment)

	f.runner.Environments = envResolver{err: errors.New("down")}
	_, err = f.runner.ResolveIn(ctx, "orders", "dev")
	require.ErrorContains(t, err, "environment")
	f.runner.Environments = envResolver{envs: map[string]appenv.Script{"prod": {}}}
	_, err = f.runner.ResolveIn(ctx, "orders", "dev")
	require.ErrorIs(t, err, ErrNoEnvironment)
	got, err := f.runner.ResolveIn(ctx, "orders", "")
	require.NoError(t, err)
	assert.Empty(t, got.Environment)
}

// An endpoint whose script no application holds answers only outside any
// environment.
func TestResolveIn_AScriptInNoApplication(t *testing.T) {
	f := newFixture(t, lookupSource)
	ctx := context.Background()
	_, err := f.runner.ResolveIn(ctx, "orders", "dev")
	require.ErrorIs(t, err, ErrNoEnvironment)
	got, err := f.runner.ResolveIn(ctx, "orders", "")
	require.NoError(t, err)
	assert.Empty(t, got.Environment)
}

// A public form's approved version runs in the same environment (#2696).
func TestTargetIn_CarriesTheEnvironmentToThePublicVersion(t *testing.T) {
	tg := &Target{public: &Target{}}
	out := tg.in(&appenv.Script{Environment: "dev", Variables: map[string]string{"A": "1"}})
	assert.Equal(t, "dev", out.Environment)
	assert.Equal(t, "dev", out.public.Environment)
	assert.Equal(t, "1", out.public.variables["A"])
	assert.Empty(t, tg.public.Environment, "the held target is not changed")
}
