package scriptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/callrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer/handlercmd"
)

type callTester struct {
	got callhandler.TestRequest
	err error
}

func (c *callTester) Test(_ context.Context, req callhandler.TestRequest) (map[string]any, error) {
	c.got = req
	return map[string]any{"outcome": "held"}, c.err
}

// TestHandlerTest is manage_script handler_test: an administrator's, asking
// the tester what every handler on a connection makes of a call.
func TestHandlerTest(t *testing.T) {
	h, _ := newHandle()
	in := manageScriptInput{Command: handlercmd.Test}
	in.Connection, in.Tool, in.AsUser, in.AsPersona, in.At = "billing-api", "api_invoke_endpoint", "jo@example.com", "marketing", "2026-11-13T15:40:00Z"
	in.Arguments = map[string]any{"method": "PUT"}

	res, _, err := h.handleHandlerTest(authorCtx(), in)
	require.NoError(t, err)
	assert.Contains(t, resultText(res), "administrator's")

	res, _, _ = h.handleHandlerTest(adminCtx(), in)
	assert.Contains(t, resultText(res), "cannot test calls")

	tst := &callTester{}
	h.SetCallTester(tst)
	res, _, _ = h.handleHandlerTest(adminCtx(), in)
	require.False(t, res.IsError, resultText(res))
	assert.Contains(t, resultText(res), `"outcome":"held"`)
	assert.Equal(t, callhandler.TestRequest{
		Connection: "billing-api", Tool: "api_invoke_endpoint", Arguments: map[string]any{"method": "PUT"},
		User: "jo@example.com", Persona: "marketing", At: "2026-11-13T15:40:00Z",
	}, tst.got)
	tst.err = errors.New("at is not a time")
	res, _, _ = h.handleHandlerTest(adminCtx(), in)
	assert.Contains(t, resultText(res), "at is not a time")
}

// TestCallHandlersRuntime is kept for the platform to place in its chain.
func TestCallHandlersRuntime(t *testing.T) {
	var none *Handle
	none.SetCallHandlers(&callrun.Runtime{})
	none.SetCallTester(&callTester{})
	assert.Nil(t, none.CallHandlers())
	h, _ := newHandle()
	rt := &callrun.Runtime{}
	h.SetCallHandlers(rt)
	assert.Same(t, rt, h.CallHandlers())
}
