package scriptrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdecide"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// decided runs decide(call) in source and returns what it returned.
func decided(t *testing.T, source string, counter scriptdecide.Counter) (starlark.Value, error) {
	t.Helper()
	var got starlark.Value
	call := map[string]any{"user": "jo@example.com", "operation": "addSubscribers", "items": 5}
	callValue, err := starlarkconv.ToStarlark(call)
	require.NoError(t, err)
	_, err = Run(context.Background(), Options{
		Name: "policy", Source: source, Caller: &statusCaller{}, Counter: counter, Call: call,
		Handler: &scriptdialect.Handler{
			Name: "decide", Args: starlark.Tuple{callValue}, Program: &scriptdialect.Program{},
			Returned: func(v starlark.Value) error { got = v; return nil },
		},
	})
	return got, err
}

func field(t *testing.T, v starlark.Value, key string) starlark.Value {
	t.Helper()
	d, ok := v.(*starlark.Dict)
	require.True(t, ok, "a decision is a dict: %v", v)
	got, found, err := d.Get(starlark.String(key))
	require.NoError(t, err)
	require.True(t, found, "no %q in %v", key, v)
	return got
}

// TestDecisions are the three things a policy returns, each a frozen dict in
// one key order.
func TestDecisions(t *testing.T) {
	v, err := decided(t, "def decide(_call):\n    return platform.allow()\n", nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String(scriptdecide.DecisionAllow), field(t, v, "decision"))

	v, err = decided(t, "def decide(_call):\n    return platform.deny(\"not on Fridays\")\n", nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("not on Fridays"), field(t, v, "reason"))
	assert.Equal(t, `{"decision": "deny", "reason": "not on Fridays"}`, v.String())

	v, err = decided(t, "def decide(call):\n    return platform.require_approval([\" carol@example.com \", \"finance\"], \"%d items\" % call[\"items\"])\n", nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "require_approval", "approvers": ["carol@example.com", "finance"], "summary": "5 items"}`, v.String())
	v, err = decided(t, "def decide(_call):\n    return platform.require_approval()\n", nil)
	require.NoError(t, err)
	assert.Equal(t, `{"decision": "require_approval", "approvers": [], "summary": ""}`, v.String(), "no approvers is an administrator")

	for name, src := range map[string]string{
		"an empty reason":        "def decide(_call):\n    return platform.deny(\" \")\n",
		"a reason that is not":   "def decide(_call):\n    return platform.deny()\n",
		"an approver not a name": "def decide(_call):\n    return platform.require_approval([1])\n",
		"allow with arguments":   "def decide(_call):\n    return platform.allow(1)\n",
		"approvers not a list":   "def decide(_call):\n    return platform.require_approval(\"a\")\n",
	} {
		_, err := decided(t, src, nil)
		require.Error(t, err, name)
	}
}

// TestCount adds this call under a key and reads the total over a window;
// outside a handler it counts only this call.
func TestCount(t *testing.T) {
	v, err := decided(t, "def decide(call):\n    return platform.deny(str(platform.count(call[\"user\"], \"1h\", add=call[\"items\"])))\n", nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("5"), field(t, v, "reason"), "no counter counts only this call")

	var got []any
	counter := func(_ context.Context, key string, window time.Duration, amount int) (int, error) {
		got = []any{key, window, amount}
		return 42, nil
	}
	v, err = decided(t, "def decide(call):\n    return platform.deny(str(platform.count(call[\"user\"] + call[\"operation\"], \"7d\")))\n", counter)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("42"), field(t, v, "reason"))
	assert.Equal(t, []any{"jo@example.comaddSubscribers", 7 * 24 * time.Hour, 1}, got)

	failing := func(context.Context, string, time.Duration, int) (int, error) { return 0, errors.New("down") }
	_, err = decided(t, "def decide(_call):\n    platform.count(\"k\", \"1h\")\n    return platform.allow()\n", failing)
	require.ErrorContains(t, err, "could not be read")

	for name, src := range map[string]string{
		"an empty key":      "def decide(_call):\n    platform.count(\"\", \"1h\")\n",
		"a negative add":    "def decide(_call):\n    platform.count(\"k\", \"1h\", add=-1)\n",
		"a bad window":      "def decide(_call):\n    platform.count(\"k\", \"soon\")\n",
		"no window":         "def decide(_call):\n    platform.count(\"k\")\n",
		"a window too long": "def decide(_call):\n    platform.count(\"k\", \"32d\")\n",
	} {
		_, err := decided(t, src, nil)
		require.Error(t, err, name)
	}
}

// TestParseWindow reads a duration or a number of days, at most 31 days.
func TestParseWindow(t *testing.T) {
	for in, want := range map[string]time.Duration{"90s": 90 * time.Second, "1h30m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "31d": 31 * 24 * time.Hour} {
		got, err := scriptdecide.ParseWindow(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "0d", "-1d", "xd", "0s", "-1h", "32d", "745h", "soon"} {
		_, err := scriptdecide.ParseWindow(in)
		require.Error(t, err, in)
	}
}
