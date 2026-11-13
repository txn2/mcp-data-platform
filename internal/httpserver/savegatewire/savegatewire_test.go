package savegatewire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// The gate is assembled from the platform's own stores and settings: the
// recordings a test names, the libraries a source loads, the answer
// contracts, and the memory budget a test or a replay is held to.
func TestGate(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	g := Gate(p)
	require.NotNil(t, g)
	assert.NotNil(t, g.Recordings, "a test's recording is read from the platform's store")
	assert.NotNil(t, g.Libraries, "a source's libraries are read from the script store")
	assert.NotNil(t, g.Contracts)
	assert.NotNil(t, g.CannotServe)
	assert.Equal(t, p.Config().Scripts.Worker.ProcessRunMemoryBudget(), g.MaxMemoryBytes)
	assert.Nil(t, g.Handlers, "no handlers are checked on a platform without a database")
	assert.Nil(t, Handlers(p), "a platform without a database keeps no handler")
	assert.Nil(t, Handlers(nil))
}
