package scriptenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func run(t *testing.T, src string, env starlark.StringDict) starlark.Value {
	t.Helper()
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", src, env)
	require.NoError(t, err)
	return globals["out"]
}

// platform.env and platform.environment read the run's environment (#2696).
func TestEnvAndEnvironment(t *testing.T) {
	env := starlark.StringDict{
		"env":         Env(map[string]string{"API_BASE": "https://x.example.com"}),
		"environment": Environment("dev"),
	}
	assert.Equal(t, starlark.String("https://x.example.com"), run(t, `out = env("API_BASE")`, env))
	assert.Equal(t, starlark.None, run(t, `out = env("MISSING")`, env))
	assert.Equal(t, starlark.String("fallback"), run(t, `out = env("MISSING", default="fallback")`, env))
	assert.Equal(t, starlark.String("dev"), run(t, `out = environment()`, env))

	none := starlark.StringDict{"env": Env(nil), "environment": Environment("")}
	assert.Equal(t, starlark.None, run(t, `out = environment()`, none))
	assert.Equal(t, starlark.None, run(t, `out = env("A")`, none))

	_, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", `env()`, env)
	require.ErrorContains(t, err, "platform.env")
	_, err = starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", `environment(1)`, env)
	require.ErrorContains(t, err, "platform.environment")
}
