package scriptsave

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// passthroughKit is a toolkit that forwards its caller's token on one
// connection.
type passthroughKit struct {
	registry.Toolkit
	connection string
}

func (k passthroughKit) ScriptRunCannotServe(connection string) bool {
	return connection == k.connection
}

type kits []registry.Toolkit

func (k kits) All() []registry.Toolkit { return k }

const callsTwoConnections = `def main():
    """Reads the references of a page and an upstream record."""
    platform.call("api_invoke_endpoint", {"connection": "platform-admin", "method": "GET", "path": "/api/v1/portal/assets/a/references"})
    platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": "/v1/accounts"})
`

// A source naming a connection a saved script's run cannot call is warned at
// save, once per such connection, and the warning refuses nothing (#2086).
func TestASaveIsWarnedOfAConnectionARunCannotCall(t *testing.T) {
	gate := &Gate{CannotServe: PassthroughConnections{Kits: kits{passthroughKit{connection: "crm"}, nil}}.CannotServe}
	res := gate.Check(context.Background(), Request{Name: "refs", Source: callsTwoConnections})
	require.Len(t, res.Lint.Warnings, 1)
	w := res.Lint.Warnings[0]
	assert.Equal(t, RulePassthroughConnection, w.Rule)
	assert.Contains(t, w.Message, `connection "crm" sends each call with the caller's own sign-in token`)
	assert.Contains(t, w.Message, "a draft run succeeds because it runs in your session")
	assert.Contains(t, res.Lint.Findings, w)
	assert.NotContains(t, res.Refusal, "crm", "a warning never refuses a save")
}

func TestPassthroughConnections_CannotServe(t *testing.T) {
	assert.False(t, PassthroughConnections{}.CannotServe("crm"), "no registry reports nothing")
	assert.False(t, PassthroughConnections{Kits: kits{passthroughKit{connection: "crm"}}}.CannotServe("platform-admin"))
	assert.True(t, PassthroughConnections{Kits: kits{passthroughKit{connection: "crm"}}}.CannotServe("crm"))
	assert.Empty(t, passthroughFindings(callsTwoConnections, nil), "a gate with no lookup warns of nothing")
}
