package scriptstmt

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// shop serves one application database, "shop".
type shop struct{}

func (shop) HasConnection(name string) bool { return name == "shop" }
func (shop) Query(context.Context, postgres.Statement) (*postgres.Result, error) {
	return &postgres.Result{}, nil
}

func (shop) Execute(context.Context, postgres.Statement) (*postgres.Result, error) {
	return &postgres.Result{}, nil
}

func (shop) Batch(context.Context, string, []postgres.Statement) ([]*postgres.Result, error) {
	return nil, nil
}

func (shop) Begin(context.Context, postgres.TxOptions) (scriptsession.AppDBTx, error) {
	return nil, nil //nolint:nilnil // never called: Call only picks the path
}

func (shop) OpenCursor(context.Context, postgres.Statement, int) (scriptsession.AppDBCursor, error) {
	return nil, nil //nolint:nilnil // never called
}

func (shop) CopyIn(context.Context, postgres.CopyIn) (int64, error) { return 0, nil }

func (shop) CopyOut(context.Context, postgres.Statement, string, io.Writer, int64) (*postgres.CopyOutResult, error) {
	return nil, nil //nolint:nilnil // never called
}

func params(t *testing.T, k string, v starlark.Value) *starlark.Dict {
	t.Helper()
	d := starlark.NewDict(1)
	require.NoError(t, d.SetKey(starlark.String(k), v))
	return d
}

func TestCall_ChoosesThePathByConnection(t *testing.T) {
	scriptsession.InstallAppDB(scriptsession.AppDB{Runner: func() scriptsession.AppDBRunner { return shop{} }})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })
	p := params(t, "r", starlark.String("west"))

	tool, call, err := Call(false, "shop", "SELECT 1 WHERE r = :r", p, 0)
	require.NoError(t, err)
	assert.Equal(t, scriptsession.AppDBQuery, tool)
	assert.Equal(t, map[string]any{"sql": "SELECT 1 WHERE r = $1", "params": []any{"west"}, "connection": "shop"}, call)

	tool, call, err = Call(true, "shop", "DELETE FROM t", nil, 9)
	require.NoError(t, err)
	assert.Equal(t, scriptsession.AppDBExecute, tool)
	assert.Equal(t, map[string]any{"sql": "DELETE FROM t", "connection": "shop", ArgTimeout: 9}, call, "no params, none sent")

	tool, call, err = Call(false, "warehouse", "SELECT 1 WHERE r = :r", p, 0)
	require.NoError(t, err)
	assert.Equal(t, ToolQuery, tool)
	assert.Equal(t, map[string]any{"sql": "SELECT 1 WHERE r = 'west'", "connection": "warehouse"}, call)

	tool, call, err = Call(true, "", "DELETE FROM t", nil, 0)
	require.NoError(t, err)
	assert.Equal(t, ToolExecute, tool)
	assert.Equal(t, map[string]any{"sql": "DELETE FROM t"}, call, "no connection, none sent")

	_, _, err = Call(false, "shop", "SELECT :missing", nil, 0)
	require.Error(t, err)
	_, _, err = Call(false, "warehouse", "SELECT :missing", nil, 0)
	require.Error(t, err)
}

func (shop) RunMaintenance(context.Context, string, string, time.Duration, time.Duration) error {
	return nil
}

func (shop) ApplyPersonas(string) []string { return nil }
