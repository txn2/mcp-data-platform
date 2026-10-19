package scriptsave

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

const echoHandler = `def handle(req):
    """Answers with what it was given."""
    return {"json": req}

def test_handle():
    """Echoes the request."""
    assert.eq(handle(1)["json"], 1)
`

func boundGate() *Gate {
	return &Gate{Endpoint: func(_ context.Context, id string) string {
		if id == "s1" {
			return "orders"
		}
		return ""
	}}
}

// #2171: saving a bound script whose source defines no handle(req) is
// refused with a message naming the missing function.
func TestABoundScriptIsSavedOnlyWithAHandler(t *testing.T) {
	res := boundGate().Check(context.Background(), Request{Existing: &script.Script{ID: "s1"}, Name: "x", Source: usesDoubler, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, `answers the endpoint "orders"`)
	assert.Contains(t, res.Refusal, "defines no handle(req)")

	res = boundGate().Check(context.Background(), Request{Existing: &script.Script{ID: "s1"}, Name: "x", Source: echoHandler, Caller: jane})
	assert.False(t, res.Refused(), res.Refusal)
}

func TestAnUnboundScriptNeedsNoHandler(t *testing.T) {
	res := boundGate().Check(context.Background(), Request{Existing: &script.Script{ID: "s2"}, Name: "x", Source: usesDoubler, Caller: jane})
	assert.NotContains(t, res.Refusal, "endpoint")
	res = boundGate().Check(context.Background(), Request{Name: "x", Source: usesDoubler, Caller: jane})
	assert.NotContains(t, res.Refusal, "endpoint", "a new script is bound to nothing")
}
