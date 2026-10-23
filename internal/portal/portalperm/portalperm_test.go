package portalperm

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func TestRefusedNamesTheAreaAPersonaLacks(t *testing.T) {
	off := false
	lacking := persona.PortalAccess{
		Assets: persona.PortalShared, Knowledge: persona.PortalRead, Catalog: persona.PortalNone, APIs: persona.PortalNone,
		Feedback: &off, Prompts: &off, Automations: &off, Search: &off, Memory: &off, Download: &off,
	}
	full := persona.FullPortalAccess()
	cases := []struct {
		method, path, want string
	}{
		{http.MethodGet, "/api/v1/portal/knowledge-pages", ""},
		{http.MethodPost, "/api/v1/portal/knowledge-pages", AreaKnowledge},
		{http.MethodPut, "/api/v1/portal/knowledge-pages/p1", AreaKnowledge},
		{http.MethodGet, "/api/v1/portal/catalog/datasets", AreaCatalog},
		{http.MethodGet, "/api/v1/apis", AreaAPIs},
		{http.MethodGet, "/api/v1/apis/billing/operations", AreaAPIs},
		{http.MethodGet, "/api/v1/portal/threads", AreaFeedback},
		{http.MethodGet, "/api/v1/portal/feedback/activity", AreaFeedback},
		{http.MethodGet, "/api/v1/portal/activity/overview", ""},
		{http.MethodGet, "/api/v1/portal/assets/a1/signoff", AreaFeedback},
		{http.MethodGet, "/api/v1/portal/prompts", AreaPrompts},
		{http.MethodGet, "/api/v1/portal/shared-prompts", AreaPrompts},
		{http.MethodGet, "/api/v1/portal/scripts", AreaAutomations},
		{http.MethodGet, "/api/v1/portal/search", AreaSearch},
		{http.MethodGet, "/api/v1/portal/memory/records", AreaMemory},
		{http.MethodGet, "/api/v1/portal/knowledge/insights", AreaMemory},
		{http.MethodPost, "/api/v1/portal/assets", AreaAssets},
		{http.MethodPost, "/api/v1/portal/collections", AreaAssets},
		{http.MethodPost, "/api/v1/portal/assets/a1/copy", AreaAssets},
		// What a person at the shared level still does: read and work on
		// what is shared with them, and everything outside an area.
		{http.MethodGet, "/api/v1/portal/assets/a1", ""},
		{http.MethodGet, "/api/v1/portal/assets/a1/content", ""},
		{http.MethodPut, "/api/v1/portal/assets/a1", ""},
		{http.MethodGet, "/api/v1/portal/shared-with-me", ""},
		{http.MethodGet, "/api/v1/portal/me", ""},
		{http.MethodGet, "/api/v1/portal/notifications", ""},
		{http.MethodGet, "/portal/view/abc", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Refused(lacking, c.method, c.path), "%s %s", c.method, c.path)
		assert.Empty(t, Refused(full, c.method, c.path), "full access refused %s %s", c.method, c.path)
	}
}

func TestReadLevelsReadAndWriteLevelsWrite(t *testing.T) {
	read := persona.PortalAccess{Knowledge: persona.PortalRead, Catalog: persona.PortalRead}
	assert.Empty(t, Refused(read, http.MethodGet, "/api/v1/portal/catalog/datasets"))
	assert.Equal(t, AreaCatalog, Refused(read, http.MethodPost, "/api/v1/portal/catalog/datasets/x/aspects"))
	none := persona.PortalAccess{Knowledge: persona.PortalNone}
	assert.Equal(t, AreaKnowledge, Refused(none, http.MethodGet, "/api/v1/portal/knowledge-pages"))
}

// A person of an external persona reaches no directory, keys or channels.
func TestRefusedWithholdsTheDirectoryFromAnExternalPersona(t *testing.T) {
	external := persona.PortalAccess{External: true}
	for _, path := range []string{"/api/v1/portal/users", "/api/v1/portal/mention-candidates", "/api/v1/portal/api-keys", "/api/v1/portal/channels"} {
		assert.Equal(t, AreaExternal, Refused(external, http.MethodGet, path), path)
		assert.Empty(t, Refused(persona.FullPortalAccess(), http.MethodGet, path), path)
	}
}

// The scratch-table routes sit outside /api/v1/portal/ and were refused
// nothing (#2257): listing needs the list level, registering the register
// level, and a resource's tables a Resources level above none.
func TestRefusedHoldsTheScratchTableRoutes(t *testing.T) {
	none := persona.PortalAccess{ScratchTables: persona.PortalNone, Resources: persona.PortalOwn}
	list := persona.PortalAccess{ScratchTables: persona.PortalList, Resources: persona.PortalOwn}
	noResources := persona.PortalAccess{ScratchTables: persona.PortalRegister, Resources: persona.PortalNone}
	cases := []struct {
		access             persona.PortalAccess
		method, path, want string
	}{
		{none, http.MethodGet, "/api/v1/tables", AreaScratchTables},
		{none, http.MethodGet, "/api/v1/tables/reg1", AreaScratchTables},
		{none, http.MethodGet, "/api/v1/table-connections", AreaScratchTables},
		{none, http.MethodGet, "/api/v1/resources/r1/tables", AreaScratchTables},
		{none, http.MethodPost, "/api/v1/portal/assets/a1/tables", AreaScratchTables},
		{list, http.MethodGet, "/api/v1/tables", ""},
		{list, http.MethodGet, "/api/v1/resources/r1/tables", ""},
		{list, http.MethodPost, "/api/v1/resources/r1/tables", AreaScratchTables},
		{list, http.MethodDelete, "/api/v1/portal/assets/a1/tables/reg1", AreaScratchTables},
		{noResources, http.MethodPost, "/api/v1/resources/r1/tables", AreaResources},
		{noResources, http.MethodPost, "/api/v1/portal/assets/a1/tables", ""},
		// Not a table route: the resources API decides these itself.
		{none, http.MethodGet, "/api/v1/resources/r1", ""},
		{none, http.MethodGet, "/api/v1/resources/tables", ""},
		{none, http.MethodGet, "/api/v1/tablesx", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Refused(c.access, c.method, c.path), "%s %s", c.method, c.path)
	}
	ext := (&persona.Persona{Name: "vendors", External: true}).PortalPermissions(nil)
	assert.Equal(t, AreaScratchTables, Refused(ext, http.MethodGet, "/api/v1/tables"), "External takes scratch tables away")
	assert.Empty(t, Refused(persona.FullPortalAccess(), http.MethodPost, "/api/v1/resources/r1/tables"))
}
