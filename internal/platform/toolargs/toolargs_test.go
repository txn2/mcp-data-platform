package toolargs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// fakeLookup resolves a tool's toolkit kind from a name->kind map, so the
// "kind:" entries in a purpose tool set can be exercised without a live
// registry.
type fakeLookup map[string]string

func (l fakeLookup) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	kind, ok := l[toolName]
	return registry.ToolkitMatch{Kind: kind, Name: "inst", Connection: "conn", Found: ok}
}

// TestSessionHandles_Defaults pins the default-on convention: an absent
// sessions.handles block mints, advertises, validates, and requires handles, and
// only an explicit false turns either off.
func TestSessionHandles_Defaults(t *testing.T) {
	var absent SessionHandles
	assert.True(t, absent.IsEnabled())
	assert.True(t, absent.IsRequired())
	assert.Equal(t, DefaultSessionHandleTTL, absent.HandleTTL())

	off := false
	on := true
	assert.False(t, SessionHandles{Enabled: &off}.IsEnabled())
	assert.True(t, SessionHandles{Enabled: &on}.IsEnabled())
	assert.False(t, SessionHandles{Require: &off}.IsRequired())
	assert.True(t, SessionHandles{Require: &on}.IsRequired())
}

// TestSessionHandles_HandleTTL proves a non-positive configured TTL falls back
// to the default rather than expiring every handle the moment it is minted.
func TestSessionHandles_HandleTTL(t *testing.T) {
	assert.Equal(t, time.Hour, SessionHandles{TTL: time.Hour}.HandleTTL())
	assert.Equal(t, DefaultSessionHandleTTL, SessionHandles{TTL: 0}.HandleTTL())
	assert.Equal(t, DefaultSessionHandleTTL, SessionHandles{TTL: -time.Hour}.HandleTTL())
}

// TestPurpose_Defaults pins the same convention for the purpose argument.
func TestPurpose_Defaults(t *testing.T) {
	var absent Purpose
	assert.True(t, absent.IsEnabled())
	assert.True(t, absent.IsRequired())

	off := false
	on := true
	assert.False(t, Purpose{Enabled: &off}.IsEnabled())
	assert.True(t, Purpose{Enabled: &on}.IsEnabled())
	assert.False(t, Purpose{Require: &off}.IsRequired())
	assert.True(t, Purpose{Require: &on}.IsRequired())
}

// TestBuildPurposeResolver proves the seam turns config into the resolver the
// facade wires on both paths: the default set gates the data-access tools and
// the gateway-proxied ones, an override replaces it wholesale, and a disabled
// block yields the nil no-op both consumers accept.
func TestBuildPurposeResolver(t *testing.T) {
	lookup := fakeLookup{"vendor__list_contacts": "mcp", "trino_query": "trino"}

	t.Run("default set", func(t *testing.T) {
		r := BuildPurposeResolver(Purpose{}, lookup)
		require.NotNil(t, r)
		assert.True(t, r.Gates("trino_query"))
		assert.True(t, r.Gates("vendor__list_contacts"), "kind:mcp reaches proxied tools")
		assert.False(t, r.Gates("platform_info"))
	})

	t.Run("configured set replaces the default", func(t *testing.T) {
		r := BuildPurposeResolver(Purpose{Tools: []string{"s3_*"}}, lookup)
		require.NotNil(t, r)
		assert.True(t, r.Gates("s3_object"))
		assert.False(t, r.Gates("trino_query"))
		assert.False(t, r.Gates("vendor__list_contacts"), "an override drops kind:mcp too")
	})

	t.Run("require is carried through", func(t *testing.T) {
		off := false
		// Require is not observable on the resolver's exported surface, so this
		// asserts the construction path runs rather than the refusal itself,
		// which pkg/middleware covers end to end.
		assert.NotNil(t, BuildPurposeResolver(Purpose{Require: &off}, lookup))
	})

	t.Run("disabled yields the nil no-op", func(t *testing.T) {
		off := false
		assert.Nil(t, BuildPurposeResolver(Purpose{Enabled: &off}, lookup))
	})
}
