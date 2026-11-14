package scriptlayer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
)

// TestThePolicyExamplesSave: each built-in policy (#2436) saves through the
// gate a person's save crosses, with the parameters get hands back, its
// tests run and covering it.
func TestThePolicyExamplesSave(t *testing.T) {
	h, _ := scheduleHandle(t)
	for _, name := range []string{"policy-rate-limit", "policy-business-hours"} {
		ex, ok := scriptexamples.Lookup(name)
		require.True(t, ok, name)
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "my-" + name, Source: ex.Source, Params: ex.Params})
		require.False(t, res.IsError, "%s: %s", name, resultText(res))
	}
}
