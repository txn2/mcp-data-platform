package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// A run that names a handler calls it with its arguments through the host a
// run is given, so its platform calls go through the Caller as main()'s do
// (#2172).
func TestRun_CallsTheHandlerThroughTheHost(t *testing.T) {
	var got starlark.Value
	_, err := Run(context.Background(), Options{
		Name: "lookup",
		Source: `
def handle(req):
    res = platform.call("api_invoke_endpoint", {"connection": "nws", "method": "GET", "path": "/" + req})
    return {"status": res["status"]}
`,
		Caller: &statusCaller{statuses: []float64{204}},
		Handler: &scriptdialect.Handler{
			Name: "handle", Args: starlark.Tuple{starlark.String("x")}, Program: &scriptdialect.Program{},
			Returned: func(v starlark.Value) error { got = v; return nil },
		},
	})
	require.NoError(t, err)
	d, ok := got.(*starlark.Dict)
	require.True(t, ok)
	status, _, _ := d.Get(starlark.String("status"))
	assert.Equal(t, starlark.MakeInt(204), status)
}
