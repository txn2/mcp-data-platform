package appenv

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

type resolver struct {
	res *Script
	err error
}

func (r resolver) ForScript(context.Context, string, string) (*Script, error) { return r.res, r.err }

// The version a run executes and the environment it reads (#2696).
func TestRunVersionIn(t *testing.T) {
	ctx := context.Background()
	sc := &script.Script{ID: "s1", Name: "api", Version: 5, ReleasedVersion: 3}
	v, err := RunVersionIn(ctx, nil, sc, "")
	require.NoError(t, err)
	assert.Equal(t, 3, v, "the last environment is the released version, read with no lookup")

	_, err = RunVersionIn(ctx, nil, sc, "dev")
	require.ErrorContains(t, err, "keeps no application environments")
	_, err = RunVersionIn(ctx, resolver{err: ErrNoEnvironment}, sc, "dev")
	require.ErrorContains(t, err, `has no environment "dev"`)
	_, err = RunVersionIn(ctx, resolver{err: errors.New("down")}, sc, "dev")
	require.ErrorContains(t, err, "down")
	_, err = RunVersionIn(ctx, resolver{res: &Script{}}, sc, "dev")
	require.ErrorContains(t, err, "belongs to no application")
	_, err = RunVersionIn(ctx, resolver{res: &Script{OK: true, Environment: "dev"}}, sc, "dev")
	require.ErrorContains(t, err, "runs no release")
	v, err = RunVersionIn(ctx, resolver{res: &Script{OK: true, Environment: "dev", Version: 4}}, sc, "dev")
	require.NoError(t, err)
	assert.Equal(t, 4, v)
	v, err = RunVersionIn(ctx, resolver{res: &Script{OK: true, Environment: "prod", Last: true}}, sc, "prod")
	require.NoError(t, err)
	assert.Equal(t, 3, v, "the last environment named runs what \"\" does, released or not (#2709)")
}

func TestForRun(t *testing.T) {
	ctx := context.Background()
	name, vars, err := ForRun(ctx, nil, "s1", "")
	require.NoError(t, err)
	assert.Empty(t, name)
	assert.Nil(t, vars)
	name, vars, err = ForRun(ctx, resolver{res: &Script{OK: true, Environment: "prod", Variables: map[string]string{"A": "1"}}}, "s1", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", name)
	assert.Equal(t, map[string]string{"A": "1"}, vars)
	name, _, err = ForRun(ctx, resolver{res: &Script{}}, "s1", "")
	require.NoError(t, err)
	assert.Empty(t, name, "a script no application holds runs in none")
	_, _, err = ForRun(ctx, resolver{err: ErrNoEnvironment}, "s1", "qa")
	require.ErrorIs(t, err, ErrNoEnvironment)
}
