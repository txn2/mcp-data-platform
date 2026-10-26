package formwire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/webhookwire"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Without a platform, or without webhooks, nothing is attached and nothing
// is read: a form stores nothing, as it did before submissions were stored.
func TestNothingWithoutAPlatform(t *testing.T) {
	Attach(nil, &webhookwire.Webhooks{})
	Attach(nil, nil)
	assert.Nil(t, ViewerKey(nil))
	assert.Nil(t, Submissions(nil))
	assert.Nil(t, scratchConnections(nil))
	assert.Nil(t, trino(nil))
}

// A platform with no database has no endpoints and no Trino toolkit: nothing
// is attached, signed or read.
func TestNothingWithoutEndpoints(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{Server: platform.ServerConfig{Name: "test"}}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	Attach(p, &webhookwire.Webhooks{})
	assert.Nil(t, ViewerKey(p))
	assert.Nil(t, Submissions(p))
	assert.Nil(t, scratchConnections(p))
}
