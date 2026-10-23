package resourcelevel

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Each resources level narrows the caller's claims as #2225 sets it out.
func TestApplyResourceLevel(t *testing.T) {
	pr := persona.NewRegistry()
	for name, level := range map[string]string{
		"none": persona.PortalNone, "shared": persona.PortalShared, "own": persona.PortalOwn, "persona": persona.PortalPersonaFolders,
	} {
		require.NoError(t, pr.Register(&persona.Persona{Name: name, Roles: []string{name}, Portal: &persona.PortalAccess{Resources: level}}))
	}
	claims := func(role string, admin bool) *resource.Claims {
		return &resource.Claims{Sub: "u1", Roles: []string{role}, Personas: []string{role}, AdminOfPersonas: []string{role}, IsAdmin: admin}
	}

	_, err := Apply(claims("none", false), pr, http.MethodGet)
	assert.True(t, errors.Is(err, resource.ErrForbidden), "none refuses a read")

	got, err := Apply(claims("shared", false), pr, http.MethodGet)
	require.NoError(t, err)
	assert.Equal(t, []string{"shared"}, got.Personas, "shared reads the persona folder")
	_, err = Apply(claims("shared", false), pr, http.MethodPost)
	assert.True(t, errors.Is(err, resource.ErrForbidden), "shared writes nothing")

	got, err = Apply(claims("own", false), pr, http.MethodPost)
	require.NoError(t, err)
	assert.Empty(t, got.Personas, "own does not reach a persona folder")
	assert.Empty(t, got.AdminOfPersonas)
	assert.Contains(t, resource.VisibleScopes(*got), resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: "u1"}, "own keeps the caller's folder")

	got, err = Apply(claims("persona", false), pr, http.MethodPost)
	require.NoError(t, err)
	assert.Equal(t, []string{"persona"}, got.Personas)

	got, err = Apply(claims("none", true), pr, http.MethodPost)
	require.NoError(t, err, "an administrator is held to none of it")
	assert.Equal(t, []string{"none"}, got.Personas)
}

// A person of an external persona never reads Global (#2257), and writes
// only when the persona lets its people upload (#2258).
func TestApplyExternalPersona(t *testing.T) {
	pr := persona.NewRegistry()
	require.NoError(t, pr.Register(&persona.Persona{Name: "vendors", Roles: []string{"vendor"}, External: true}))
	require.NoError(t, pr.Register(&persona.Persona{
		Name: "agency", Roles: []string{"agency"}, External: true,
		Portal: &persona.PortalAccess{Uploads: &persona.PortalUploads{SharePersonas: []string{"marketing"}}},
	}))
	claims := func(role string) *resource.Claims {
		return &resource.Claims{Sub: "ext:1", Roles: []string{role}, Personas: []string{role}}
	}

	got, err := Apply(claims("vendor"), pr, http.MethodGet)
	require.NoError(t, err)
	assert.True(t, got.NoGlobal)
	scopes := resource.VisibleScopes(*got)
	assert.NotContains(t, scopes, resource.ScopeFilter{Scope: resource.ScopeGlobal})
	assert.Contains(t, scopes, resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: "ext:1"})
	assert.False(t, resource.CanReadResource(*got, &resource.Resource{Scope: resource.ScopeGlobal}))
	_, err = Apply(claims("vendor"), pr, http.MethodPost)
	assert.True(t, errors.Is(err, resource.ErrForbidden), "no uploads setting, no upload")

	got, err = Apply(claims("agency"), pr, http.MethodPost)
	require.NoError(t, err, "uploads let the person write to their own folder")
	assert.True(t, got.NoGlobal)
	assert.False(t, resource.CanWriteScope(*got, resource.ScopePersona, "agency"), "never the persona folder")
	assert.True(t, resource.CanWriteScope(*got, resource.ScopeUser, "ext:1"))
}
