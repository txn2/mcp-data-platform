package scriptsave

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const echoHandler = `def handle(req):
    """Answers with what it was given."""
    return {"json": req}

def test_handle():
    """Echoes the request."""
    assert.eq(handle(1)["json"], 1)
`

// handlersGate is a gate whose handler check refuses, for script s1, a source
// defining no handle(req), and records what each check was asked about.
type handlersGate struct {
	asked []string
	saw   []string
}

func (h *handlersGate) gate() *Gate {
	return &Gate{Handlers: func(_ context.Context, sc *script.Script, source string) string {
		h.asked = append(h.asked, sc.ID)
		h.saw = append(h.saw, source)
		if sc.ID != "s1" {
			return ""
		}
		if err := epbinding.Callable(source); err != nil {
			return "this script answers the endpoint \"orders\" (endpoint handler ep1): " + err.Error() + ", so it was not saved"
		}
		return ""
	}}
}

// #2171, #2441: saving a script whose handlers could not serve the new
// source is refused with the handler check's own words; the check is asked
// about the script being saved and the source as lint formatted it.
func TestAScriptIsSavedOnlyWhenItsHandlersCanServeIt(t *testing.T) {
	h := &handlersGate{}
	res := h.gate().Check(context.Background(), Request{Existing: &script.Script{ID: "s1"}, Name: "x", Source: usesDoubler, Caller: jane})
	require.True(t, res.Refused())
	assert.Equal(t, `this script answers the endpoint "orders" (endpoint handler ep1): `+
		epbinding.Callable(res.Lint.Source).Error()+`, so it was not saved`, res.Refusal)
	assert.Contains(t, res.Refusal, "defines no handle(req)")
	assert.Equal(t, []string{"s1"}, h.asked)
	assert.Equal(t, []string{res.Lint.Source}, h.saw, "the check reads the formatted source the save stores")

	res = h.gate().Check(context.Background(), Request{Existing: &script.Script{ID: "s1"}, Name: "x", Source: echoHandler, Caller: jane})
	assert.False(t, res.Refused(), res.Refusal)
}

func TestAScriptWithNoHandlerNeedsNone(t *testing.T) {
	h := &handlersGate{}
	res := h.gate().Check(context.Background(), Request{Existing: &script.Script{ID: "s2"}, Name: "x", Source: usesDoubler, Caller: jane})
	assert.NotContains(t, res.Refusal, "endpoint")
	assert.Equal(t, []string{"s2"}, h.asked)

	res = h.gate().Check(context.Background(), Request{Name: "x", Source: usesDoubler, Caller: jane})
	assert.NotContains(t, res.Refusal, "endpoint", "a new script holds no handler")
	assert.Equal(t, []string{"s2"}, h.asked, "and the check is not asked about it")

	res = (&Gate{}).Check(context.Background(), Request{Existing: &script.Script{ID: "s1"}, Name: "x", Source: usesDoubler, Caller: jane})
	assert.NotContains(t, res.Refusal, "endpoint", "no check wired checks nothing")
}
