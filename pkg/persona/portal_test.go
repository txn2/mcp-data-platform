package persona

import (
	"slices"
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

// The Sites section follows the tool an area is reached through: on with
// manage_site, off without it, and a block's own setting wins.
func TestPortalPermissionsSitesFollowManageSite(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name    string
		persona *Persona
		granted []string
		want    bool
	}{
		{"unset with manage_site", &Persona{Name: "p"}, []string{"manage_site", "search"}, true},
		{"unset without it", &Persona{Name: "p"}, []string{"search"}, false},
		{"no tools at all", &Persona{Name: "p"}, nil, false},
		{"a nil persona", nil, []string{"manage_site"}, true},
		{"set on without the tool", &Persona{Name: "p", Portal: &PortalAccess{Sites: &on}}, nil, true},
		{"set off with the tool", &Persona{Name: "p", Portal: &PortalAccess{Sites: &off}}, []string{"manage_site"}, false},
		{"external with the tool", &Persona{Name: "p", External: true}, []string{"manage_site"}, false},
		{"external, set on", &Persona{Name: "p", External: true, Portal: &PortalAccess{Sites: &on}}, []string{"manage_site"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.persona.PortalPermissions(c.granted)
			require.NotNil(t, got.Sites, "every switch is set")
			assert.Equal(t, c.want, *got.Sites)
		})
	}
	assert.True(t, On(FullPortalAccess().Sites), "an administrator has the section")
}

func TestToolOr(t *testing.T) {
	on, off := true, false
	assert.Same(t, &on, toolOr(&on, false), "what is set is returned")
	assert.Same(t, &off, toolOr(&off, true))
	assert.True(t, *toolOr(nil, true), "unset follows the tool")
	assert.False(t, *toolOr(nil, false))
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

// External takes scratch tables away and keeps its uploads switch, unset
// reading off; an internal persona's uploads setting is dropped (#2257,
// #2258, #2290).
func TestPortalScratchTablesAndUploads(t *testing.T) {
	got := (&Persona{Name: "p"}).PortalPermissions(nil)
	assert.Equal(t, PortalRegister, got.ScratchTables, "unset keeps what every persona had before #2257")
	assert.Nil(t, got.Uploads)

	on := true
	ext := &Persona{Name: "vendors", External: true, Portal: &PortalAccess{ScratchTables: PortalRegister, Uploads: &on}}
	eff := ext.PortalPermissions(nil)
	assert.Equal(t, PortalNone, eff.ScratchTables)
	require.NotNil(t, eff.Uploads)
	assert.True(t, *eff.Uploads)
	unset := (&Persona{Name: "v", External: true}).PortalPermissions(nil)
	require.NotNil(t, unset.Uploads)
	assert.False(t, *unset.Uploads, "an external persona uploads only when it says so")

	internal := &Persona{Name: "staff", Portal: &PortalAccess{Uploads: &on}}
	assert.Nil(t, internal.PortalPermissions(nil).Uploads, "uploads are an external persona's setting")

	assert.Equal(t, PortalRegister, FullPortalAccess().ScratchTables)
	assert.True(t, AtLeast(PortalRegister, PortalList))
	assert.False(t, AtLeast(PortalNone, PortalList))

	require.NoError(t, (&PortalAccess{ScratchTables: PortalList}).Validate())
	require.Error(t, (&PortalAccess{ScratchTables: PortalWrite}).Validate())
}

// Earlier versions reads off unless a persona turns it on, and an
// administrator has it; External files reads none, an external persona never
// has it, and an administrator manages (#2290).
func TestPortalHistoryAndExternalFiles(t *testing.T) {
	got := (&Persona{Name: "p"}).PortalPermissions(nil)
	require.NotNil(t, got.History)
	assert.False(t, *got.History)
	assert.Equal(t, PortalNone, got.ExternalFiles)

	on := true
	staff := (&Persona{Name: "s", Portal: &PortalAccess{History: &on, ExternalFiles: PortalView}}).PortalPermissions(nil)
	assert.True(t, *staff.History)
	assert.Equal(t, PortalView, staff.ExternalFiles)

	ext := (&Persona{Name: "c", External: true, Portal: &PortalAccess{History: &on, ExternalFiles: PortalManage}}).PortalPermissions(nil)
	assert.True(t, *ext.History, "an external persona's earlier versions are its own setting")
	assert.Equal(t, PortalNone, ext.ExternalFiles)

	full := FullPortalAccess()
	assert.True(t, *full.History)
	assert.Equal(t, PortalManage, full.ExternalFiles)

	assert.True(t, AtLeast(PortalManage, PortalView))
	assert.False(t, AtLeast(PortalView, PortalManage))
	require.NoError(t, (&PortalAccess{ExternalFiles: PortalManage}).Validate())
	require.Error(t, (&PortalAccess{ExternalFiles: PortalWrite}).Validate())

	stored := (&Persona{Name: "c", Portal: &PortalAccess{ExternalFiles: PortalView}}).StoredPortal(nil)
	assert.Contains(t, Differs(stored, ext), "external_files")
}

// Differs names what an external persona's limits took from its stored
// levels, which the editor shows beside each locked row (#2257).
func TestPortalDiffers(t *testing.T) {
	stored := (&Persona{Name: "p", Portal: &PortalAccess{Knowledge: PortalWrite}}).PortalPermissions(nil)
	eff := (&Persona{Name: "p", External: true, Portal: &PortalAccess{Knowledge: PortalWrite}}).PortalPermissions(nil)
	got := Differs(stored, eff)
	assert.Contains(t, got, "knowledge")
	assert.Contains(t, got, "scratch_tables")
	assert.Contains(t, got, "search")
	assert.NotContains(t, got, "feedback")
	assert.Empty(t, Differs(stored, stored))
}

func TestValidateAccount(t *testing.T) {
	require.NoError(t, ValidateAccount(nil, "admin"))
	require.NoError(t, ValidateAccount(&Persona{Name: "admin"}, "admin"))
	require.ErrorIs(t, ValidateAccount(&Persona{Name: "admin", External: true}, "admin"), ErrExternalAdmin)
	require.NoError(t, ValidateAccount(&Persona{Name: "admin", External: true}, ""), "no administrator configured")
	require.ErrorIs(t, ValidateAccount(&Persona{Name: "x", External: true, ServiceAccount: true}, "admin"), ErrAccountType)
	on, off := true, false
	require.ErrorIs(t, ValidateAccount(&Persona{Name: "x", Portal: &PortalAccess{Uploads: &on}}, "admin"), ErrUploadsInternal)
	require.NoError(t, ValidateAccount(&Persona{Name: "x", Portal: &PortalAccess{Uploads: &off}}, "admin"), "uploads off is every internal persona's")
}

// IsAdminName is AdminRule for a resolved persona's name: an external
// persona carrying the administrator's name is not the administrator.
func TestRegistryIsAdminName(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(&Persona{Name: "admin", Roles: []string{"admin"}}))
	assert.True(t, r.IsAdminName("admin", "admin"))
	assert.False(t, r.IsAdminName("admin", ""))
	assert.False(t, r.IsAdminName("analyst", "admin"))
	assert.False(t, r.IsAdminName("other", "other"), "not registered")
	require.NoError(t, r.Register(&Persona{Name: "admin", Roles: []string{"admin"}, External: true}))
	assert.False(t, r.IsAdminName("admin", "admin"))
	var none *Registry
	assert.False(t, none.IsAdminName("admin", "admin"))
}

// The Sites area reads from a tool registered on the platform, so a persona's
// toolkit tools alone do not say whether it was granted (#2559).
func TestPortalGrants(t *testing.T) {
	allowed := &Persona{Name: "builder", Tools: ToolRules{Allow: []string{"*"}}}
	denied := &Persona{Name: "viewer", Tools: ToolRules{Allow: []string{"trino_*"}}}
	input := []string{"trino_query"}

	got := PortalGrants(allowed, input)
	if !slices.Contains(got, ToolManageSite) || !slices.Contains(got, "trino_query") {
		t.Fatalf("allowed: %v", got)
	}
	if slices.Contains(input, ToolManageSite) {
		t.Fatal("the caller's slice was changed")
	}
	if got := PortalGrants(denied, input); slices.Contains(got, ToolManageSite) {
		t.Fatalf("denied: %v", got)
	}
	if !On(allowed.PortalPermissions(PortalGrants(allowed, nil)).Sites) || On(denied.PortalPermissions(PortalGrants(denied, nil)).Sites) {
		t.Fatal("the Sites area does not follow the grant")
	}
}
