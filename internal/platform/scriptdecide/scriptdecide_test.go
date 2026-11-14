package scriptdecide

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func call(t *testing.T, name string, fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error), args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	t.Helper()
	return starlark.Call(&starlark.Thread{Name: "test"}, starlark.NewBuiltin(name, fn), args, kwargs) //nolint:wrapcheck // the test reads the binding's own error
}

// TestDecisions are the three things decide(call) returns, each a frozen
// dict in one key order, and the arguments each refuses.
func TestDecisions(t *testing.T) {
	v, err := call(t, CapabilityAllow, Allow, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "allow"}`, v.String())
	v, err = call(t, CapabilityDeny, Deny, starlark.Tuple{starlark.String("not on Fridays")}, nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "deny", "reason": "not on Fridays"}`, v.String())
	approvers := starlark.NewList([]starlark.Value{starlark.String(" carol@example.com "), starlark.String("finance")})
	v, err = call(t, CapabilityRequireApproval, RequireApproval, starlark.Tuple{approvers, starlark.String("5 items")}, nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "require_approval", "approvers": ["carol@example.com", "finance"], "summary": "5 items"}`, v.String())
	v, err = call(t, CapabilityRequireApproval, RequireApproval, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "require_approval", "approvers": [], "summary": ""}`, v.String())
	d, ok := v.(*starlark.Dict)
	require.True(t, ok)
	require.Error(t, d.SetKey(starlark.String("x"), starlark.None), "a decision is frozen")

	_, err = call(t, CapabilityAllow, Allow, starlark.Tuple{starlark.MakeInt(1)}, nil)
	require.Error(t, err)
	_, err = call(t, CapabilityDeny, Deny, starlark.Tuple{starlark.String(" ")}, nil)
	require.ErrorContains(t, err, "reason is empty")
	_, err = call(t, CapabilityDeny, Deny, nil, nil)
	require.Error(t, err)
	_, err = call(t, CapabilityRequireApproval, RequireApproval, starlark.Tuple{starlark.NewList([]starlark.Value{starlark.MakeInt(1)})}, nil)
	require.ErrorContains(t, err, "approvers are addresses or persona names")
	_, err = call(t, CapabilityRequireApproval, RequireApproval, starlark.Tuple{starlark.String("a")}, nil)
	require.Error(t, err)
}

// TestCount adds this call under a key and reads the total over a window;
// with no counter it counts only this call.
func TestCount(t *testing.T) {
	count := func(counter Counter, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return call(t, CapabilityCount, func(_ *starlark.Thread, b *starlark.Builtin, a starlark.Tuple, k []starlark.Tuple) (starlark.Value, error) {
			return Count(context.Background(), counter, b, a, k)
		}, args, kwargs)
	}
	v, err := count(nil, starlark.Tuple{starlark.String("jo"), starlark.String("1h")}, []starlark.Tuple{{starlark.String("add"), starlark.MakeInt(5)}})
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt(5), v)

	var got []any
	counter := func(_ context.Context, key string, window time.Duration, amount int) (int, error) {
		got = []any{key, window, amount}
		return 42, nil
	}
	v, err = count(counter, starlark.Tuple{starlark.String("jo:addSubscribers"), starlark.String("7d")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt(42), v)
	assert.Equal(t, []any{"jo:addSubscribers", 7 * 24 * time.Hour, 1}, got)

	failing := func(context.Context, string, time.Duration, int) (int, error) { return 0, errors.New("down") }
	_, err = count(failing, starlark.Tuple{starlark.String("k"), starlark.String("1h")}, nil)
	require.ErrorContains(t, err, "could not be read")
	for name, args := range map[string]starlark.Tuple{
		"an empty key":      {starlark.String(""), starlark.String("1h")},
		"a bad window":      {starlark.String("k"), starlark.String("soon")},
		"no window":         {starlark.String("k")},
		"a window too long": {starlark.String("k"), starlark.String("32d")},
	} {
		_, err := count(nil, args, nil)
		require.Error(t, err, name)
	}
	_, err = count(nil, starlark.Tuple{starlark.String("k"), starlark.String("1h")}, []starlark.Tuple{{starlark.String("add"), starlark.MakeInt(-1)}})
	require.ErrorContains(t, err, "only grows")
}

// TestParseWindow reads a duration or a number of days, at most 31 days.
func TestParseWindow(t *testing.T) {
	for in, want := range map[string]time.Duration{"90s": 90 * time.Second, "1h30m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "31d": maxCountWindow} {
		got, err := ParseWindow(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	padded, err := ParseWindow("\t7d\n")
	require.NoError(t, err)
	assert.Equal(t, 7*24*time.Hour, padded, "a window is read with its whitespace trimmed")
	for _, in := range []string{"", "0d", "-1d", "xd", "0s", "-1h", "32d", "745h", "soon"} {
		_, err := ParseWindow(in)
		require.Error(t, err, in)
	}
}

// TestQueue keeps a handler's messages, at most MaxNotices.
func TestQueue(t *testing.T) {
	var notices []map[string]any
	b := starlark.NewBuiltin("platform.notify", nil)
	for i := range MaxNotices {
		v, err := Queue(b, &notices, map[string]any{"title": i})
		require.NoError(t, err)
		assert.Equal(t, `{"queued": True}`, v.String())
	}
	_, err := Queue(b, &notices, map[string]any{"title": "one too many"})
	require.ErrorContains(t, err, "at most 5")
	assert.Len(t, notices, MaxNotices)
}
