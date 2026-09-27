package adminwire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// A deployment without a database holds none of the optional stores, and
// each field is left a true nil rather than an interface wrapping a nil
// store, which is what keeps their routes unregistered. The attached case
// needs a database and is exercised by the admin memory acceptance test.
func TestStoreDeps_NoDatabaseAttachesNothing(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "adminwire-test", Transport: "http"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	var deps admin.Deps
	StoreDeps(&deps, p)
	assert.Nil(t, deps.APICatalogStore)
	assert.Nil(t, deps.EmbedJobs)
	assert.Nil(t, deps.IndexJobs)
	assert.Nil(t, deps.MemoryRecords)
}
