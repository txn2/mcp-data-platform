package handlercmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

type tester struct {
	got callhandler.TestRequest
	err error
}

func (t *tester) Test(_ context.Context, req callhandler.TestRequest) (map[string]any, error) {
	t.got = req
	return map[string]any{"outcome": "runs"}, t.err
}

// TestRunTest is an administrator's, asks the tester, and names a refusal.
func TestRunTest(t *testing.T) {
	s := CallSettings{Tool: "api_invoke_endpoint", Arguments: map[string]any{"method": "PUT"}, AsUser: "jo@example.com", At: "2026-11-13T15:40:00Z"}
	_, refusal := RunTest(context.Background(), false, &tester{}, "c", s)
	assert.Contains(t, refusal, "administrator's")
	_, refusal = RunTest(context.Background(), true, nil, "c", s)
	assert.Contains(t, refusal, "cannot test calls")
	tst := &tester{}
	out, refusal := RunTest(context.Background(), true, tst, "c", s)
	require.Empty(t, refusal)
	assert.Equal(t, "runs", out["outcome"])
	assert.Equal(t, callhandler.TestRequest{
		Connection: "c", Tool: "api_invoke_endpoint", Arguments: map[string]any{"method": "PUT"},
		User: "jo@example.com", At: "2026-11-13T15:40:00Z",
	}, tst.got)
	tst.err = errors.New("at is not a time")
	_, refusal = RunTest(context.Background(), true, tst, "c", s)
	assert.Equal(t, "at is not a time", refusal)
}

// TestPatch carries only what was sent, the connection only when named.
func TestPatch(t *testing.T) {
	ops := []string{"a"}
	p := CallSettings{Operations: &ops}.Patch("")
	assert.Nil(t, p.Connection)
	assert.Equal(t, &ops, p.Operations)
	p = CallSettings{}.Patch("crm")
	require.NotNil(t, p.Connection)
	assert.Equal(t, "crm", *p.Connection)
}

// TestWithheld keeps that a watch's run ran and how, never what it held.
func TestWithheld(t *testing.T) {
	out := Withheld(map[string]any{"name": "watch", "run_id": "r1", "status": "succeeded", "call": map[string]any{"user": "jo"}, "log": "printed"})
	assert.Equal(t, "watch", out["name"])
	assert.Equal(t, "r1", out["run_id"])
	assert.NotContains(t, out, "call")
	assert.NotContains(t, out, "log")
	assert.Contains(t, out, "withheld")
}

// TestProperties describes every handler and tool-call setting.
func TestProperties(t *testing.T) {
	props := Properties()
	for _, k := range []string{"kind", "handler", "operations", "conditions", "protected", "approve", "tool", "as_persona"} {
		assert.Contains(t, props, k)
	}
	assert.Contains(t, Commands, Test)
	assert.Equal(t, "handler_set with kind=schedule", Removed["schedule_set"])
}
