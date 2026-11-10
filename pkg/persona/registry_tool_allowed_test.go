package persona

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryToolAllowed(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(&Persona{Name: "editor", Tools: ToolRules{Allow: []string{"*"}, Deny: []string{"manage_site"}}}))
	require.NoError(t, r.Register(&Persona{Name: "builder", Tools: ToolRules{Allow: []string{"manage_*"}}}))

	assert.False(t, r.ToolAllowed("editor", "manage_site"), "a deny wins over an allow of everything")
	assert.True(t, r.ToolAllowed("editor", "manage_asset"))
	assert.True(t, r.ToolAllowed("builder", "manage_site"))
	assert.False(t, r.ToolAllowed("nobody", "manage_site"), "an unknown persona is refused")
	var none *Registry
	assert.False(t, none.ToolAllowed("editor", "manage_site"))
}
