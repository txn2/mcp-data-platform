package persona

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A persona with no portal block keeps what its tools reached in the portal
// before #2225.
func TestPortalPermissionsResolveFromTheTools(t *testing.T) {
	cases := []struct {
		name      string
		granted   []string
		knowledge string
		catalog   string
	}{
		{"apply_knowledge and fetch", []string{"apply_knowledge", "fetch", "search"}, PortalWrite, PortalWrite},
		{"fetch alone", []string{"fetch"}, PortalRead, PortalRead},
		{"apply_knowledge alone", []string{"apply_knowledge"}, PortalWrite, PortalNone},
		{"neither", []string{"search"}, PortalRead, PortalNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (&Persona{Name: "p"}).PortalPermissions(c.granted)
			assert.Equal(t, c.knowledge, got.Knowledge)
			assert.Equal(t, c.catalog, got.Catalog)
			assert.Equal(t, PortalOwn, got.Assets)
			assert.Equal(t, PortalPersonaFolders, got.Resources)
			assert.Equal(t, PortalBrowse, got.APIs)
			for _, on := range []*bool{got.Feedback, got.Prompts, got.Automations, got.Search, got.Memory, got.Download} {
				require.NotNil(t, on, "every switch is set")
				assert.True(t, *on)
			}
		})
	}
}

// A field the block sets wins over its tools; a field it leaves unset still
// resolves.
func TestPortalPermissionsKeepWhatTheBlockSets(t *testing.T) {
	off := false
	p := &Persona{Name: "p", Portal: &PortalAccess{Knowledge: PortalNone, Resources: PortalOwn, Automations: &off}}
	got := p.PortalPermissions([]string{"apply_knowledge", "fetch"})
	assert.Equal(t, PortalNone, got.Knowledge, "set, so the tool does not decide")
	assert.Equal(t, PortalOwn, got.Resources)
	assert.False(t, On(got.Automations))
	assert.Equal(t, PortalWrite, got.Catalog, "unset, so the tools decide")
	assert.True(t, On(got.Search))

	var none *Persona
	assert.Equal(t, PortalRead, none.PortalPermissions(nil).Knowledge, "a nil persona resolves with no tool")
}

func TestPortalValidateRefusesALevelTheAreaDoesNotHave(t *testing.T) {
	require.NoError(t, (*PortalAccess)(nil).Validate())
	require.NoError(t, (&PortalAccess{}).Validate())
	require.NoError(t, (&PortalAccess{Assets: PortalShared, Resources: PortalPersonaFolders, Knowledge: PortalWrite, Catalog: PortalNone, APIs: PortalBrowse}).Validate())
	for _, bad := range []PortalAccess{
		{Assets: PortalNone}, {Resources: PortalWrite}, {Knowledge: PortalOwn}, {Catalog: PortalBrowse}, {APIs: PortalRead},
	} {
		assert.Error(t, bad.Validate(), "%+v", bad)
	}
	assert.Error(t, NewRegistry().Register(&Persona{Name: "p", Portal: &PortalAccess{Knowledge: "edit"}}),
		"the registry refuses it too, so a file persona with a bad level is refused at load")
}

func TestPortalLevelsAreOrdered(t *testing.T) {
	assert.True(t, AtLeast(PortalWrite, PortalRead))
	assert.True(t, AtLeast(PortalRead, PortalRead))
	assert.False(t, AtLeast(PortalNone, PortalRead))
	assert.True(t, AtLeast(PortalOwn, PortalShared))
	assert.False(t, AtLeast(PortalShared, PortalOwn))
	assert.False(t, AtLeast("", PortalRead), "an unresolved level reaches nothing")

	full := FullPortalAccess()
	assert.Equal(t, PortalWrite, full.Knowledge)
	assert.Equal(t, PortalPersonaFolders, full.Resources)
	assert.True(t, On(full.Download))
	assert.True(t, On(nil), "an unset switch is on")
}

// An external persona is held to #2092's limits whatever its block says, and
// calls no tool and shares nothing.
func TestExternalPersonaLimits(t *testing.T) {
	on := true
	p := &Persona{
		Name: "client", External: true, Tools: ToolRules{Allow: []string{"*"}},
		Portal: &PortalAccess{Assets: PortalOwn, Resources: PortalPersonaFolders, Knowledge: PortalWrite, Search: &on, Feedback: &on},
	}
	got := p.PortalPermissions([]string{"apply_knowledge", "fetch"})
	assert.True(t, got.External)
	assert.Equal(t, PortalShared, got.Assets)
	assert.Equal(t, PortalShared, got.Resources)
	assert.Equal(t, PortalNone, got.Knowledge)
	assert.Equal(t, PortalNone, got.Catalog)
	assert.Equal(t, PortalNone, got.APIs)
	for _, off := range []*bool{got.Search, got.Prompts, got.Automations, got.Memory} {
		assert.False(t, On(off))
	}
	assert.True(t, On(got.Feedback), "feedback stays as the persona sets it")
	assert.True(t, On(got.Download))

	none := (&Persona{Name: "c", External: true, Portal: &PortalAccess{Resources: PortalNone}}).PortalPermissions(nil)
	assert.Equal(t, PortalNone, none.Resources, "none stays none")

	again := (&Persona{Portal: &got}).PortalPermissions(nil)
	assert.Equal(t, got, again, "a resolved external block reads the same again")

	assert.False(t, NewToolFilter(nil).IsAllowed(p, "search"), "an external persona calls no tool")
	assert.False(t, p.SharingAllowed())

	r := NewRegistry()
	require.NoError(t, r.Register(&Persona{Name: "admin", Roles: []string{"admin"}, External: true}))
	assert.False(t, r.AdminRule("admin")([]string{"admin"}), "an external persona administers nothing")
}

// A block resolved on its own reads like the persona's: an unset area from
// the tools, and an external block keeps its limits.
func TestPortalAccessResolve(t *testing.T) {
	got := PortalAccess{Assets: PortalShared}.Resolve([]string{"fetch"})
	if got.Assets != PortalShared || got.Catalog != PortalRead || got.Knowledge != PortalRead {
		t.Errorf("resolved %+v, want assets shared, catalog and knowledge read", got)
	}
	ext := PortalAccess{External: true, Knowledge: PortalWrite}.Resolve([]string{"apply_knowledge"})
	if ext.Knowledge != PortalNone || !ext.External {
		t.Errorf("external block resolved %+v, want knowledge none and external", ext)
	}
}
