package integration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestHandler pins #2659 in the worker: an integration's run calls
// the operation's function with the request its call carries, keeps what it
// returned checked against that request, and fails on a value of the wrong
// shape, naming the contract.
func TestHandler(t *testing.T) {
	spec, _ := SpecOf(OpSiteRouting)
	call := RunCall(spec, Request{
		Reason: ReasonTimer, Items: []map[string]any{{"hostname": "a.example.com"}},
	})
	var kept json.RawMessage
	h, err := Handler(call, &kept)
	require.NoError(t, err)
	assert.Equal(t, "route", h.Name)
	require.Len(t, h.Args, 1)
	req, ok := h.Args[0].(*starlark.Dict)
	require.True(t, ok)
	reason, _, _ := req.Get(starlark.String("reason"))
	assert.Equal(t, starlark.String("timer"), reason)
	added, _, _ := req.Get(starlark.String("added"))
	list, isList := added.(*starlark.List)
	require.True(t, isList, "added is a list, never None")
	assert.Equal(t, 0, list.Len())

	ret := starlark.NewDict(1)
	entry := starlark.NewDict(2)
	require.NoError(t, entry.SetKey(starlark.String("status"), starlark.String("ready")))
	require.NoError(t, entry.SetKey(starlark.String("message"), starlark.String("routed")))
	require.NoError(t, ret.SetKey(starlark.String("a.example.com"), entry))
	require.NoError(t, h.Returned(ret))
	assert.JSONEq(t, `{"a.example.com":{"status":"ready","message":"routed"}}`, string(kept))

	err = h.Returned(starlark.String("nope"))
	require.ErrorContains(t, err, "route(req)")

	_, err = Handler(map[string]any{}, &kept)
	require.ErrorContains(t, err, "cannot start")
}

// TestPrepare: an integration's run calls the operation's function and keeps
// its checked return as the result; another run is left alone; a run whose
// call names no operation cannot start.
func TestPrepare(t *testing.T) {
	spec, _ := SpecOf(OpSiteRouting)
	call := RunCall(spec, Request{Reason: ReasonTimer, Items: []map[string]any{{"hostname": "a.example.com"}}})
	opts := &scriptrun.Options{Call: call}
	finish, err := Prepare(runstate.TriggerIntegration, opts)
	require.NoError(t, err)
	assert.Nil(t, opts.Call, "run.call is not the request: route(req) is handed it")
	require.NotNil(t, opts.Handler)
	ret := starlark.NewDict(1)
	entry := starlark.NewDict(1)
	require.NoError(t, entry.SetKey(starlark.String("status"), starlark.String("ready")))
	require.NoError(t, ret.SetKey(starlark.String("a.example.com"), entry))
	require.NoError(t, opts.Handler.Returned(ret))
	res := &script.RunResult{Status: script.RunStatusSucceeded}
	finish(res)
	assert.JSONEq(t, `{"a.example.com":{"status":"ready","message":""}}`, string(res.Result))
	failed := &script.RunResult{Status: script.RunStatusFailed}
	finish(failed)
	assert.Nil(t, failed.Result)

	other := &scriptrun.Options{Call: map[string]any{"tool": "x"}}
	finish, err = Prepare("watch", other)
	require.NoError(t, err)
	assert.Nil(t, other.Handler)
	assert.NotNil(t, other.Call)
	r := &script.RunResult{Status: script.RunStatusSucceeded}
	finish(r)
	assert.Nil(t, r.Result)

	_, err = Prepare(runstate.TriggerIntegration, &scriptrun.Options{Call: map[string]any{}})
	require.Error(t, err)
}
