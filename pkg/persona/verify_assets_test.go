package persona

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyAssetsResolves(t *testing.T) {
	yes, no := true, false
	unset := (&Persona{}).PortalPermissions(nil)
	require.NotNil(t, unset.VerifyAssets)
	assert.False(t, *unset.VerifyAssets, "unset is off: only administrators verify")

	set := (&Persona{Portal: &PortalAccess{VerifyAssets: &yes}}).PortalPermissions(nil)
	assert.True(t, *set.VerifyAssets)

	off := (&Persona{Portal: &PortalAccess{VerifyAssets: &no}}).PortalPermissions(nil)
	assert.False(t, *off.VerifyAssets)

	external := (&Persona{External: true, Portal: &PortalAccess{VerifyAssets: &yes}}).PortalPermissions(nil)
	assert.False(t, *external.VerifyAssets, "an external persona never verifies, whatever it sets")

	full := FullPortalAccess()
	require.NotNil(t, full.VerifyAssets)
	assert.True(t, *full.VerifyAssets, "an administrator verifies")
}

func TestRegistryVerifyAllowed(t *testing.T) {
	var nilRegistry *Registry
	assert.False(t, nilRegistry.VerifyAllowed("anyone"))

	yes := true
	reg := NewRegistry()
	require.NoError(t, reg.Register(&Persona{Name: "verifiers", Roles: []string{"v"}, Portal: &PortalAccess{VerifyAssets: &yes}}))
	require.NoError(t, reg.Register(&Persona{Name: "analysts", Roles: []string{"a"}}))
	require.NoError(t, reg.Register(&Persona{Name: "partners", Roles: []string{"p"}, External: true, Portal: &PortalAccess{VerifyAssets: &yes}}))

	assert.True(t, reg.VerifyAllowed("verifiers"))
	assert.False(t, reg.VerifyAllowed("analysts"), "unset is off")
	assert.False(t, reg.VerifyAllowed("partners"), "external personas never verify")
	assert.False(t, reg.VerifyAllowed("nobody"), "an unknown persona")
}
