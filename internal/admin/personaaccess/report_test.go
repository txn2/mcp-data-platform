package personaaccess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// inv is a deployment with a Trino toolkit over two connections, one with a
// scratch catalog, an api connection with two operations, and the platform's
// search tool.
func inv() Inventory {
	return Inventory{
		Tools: []Tool{
			{Name: "search"},
			{Name: "trino_query", Kind: "trino"},
			{Name: "trino_execute", Kind: "trino"},
			{Name: "api_invoke_endpoint", Kind: "api"},
		},
		Connections: []Connection{
			{Name: "acme-trino", Kind: "trino", Scratch: &Scratch{Catalog: "scratch", Schema: "uploads", Writes: true}},
			{Name: "warehouse", Kind: "trino"},
			{Name: "crm-api", Kind: "api", Channels: []string{"ops"}},
		},
		Operations: []Operation{
			{Connection: "crm-api", Method: "GET", Path: "/contacts"},
			{Connection: "crm-api", Method: "DELETE", Path: "/contacts/{id}"},
		},
		Registrations: map[string]int{"acme-trino": 14},
	}
}

func everything(name string) *persona.Persona {
	return &persona.Persona{
		Name: name, Roles: []string{name},
		Tools:       persona.ToolRules{Allow: []string{"*"}},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}
}

// An external persona whose rules allow everything calls no tool, no
// operation and no scratch table (#2257).
func TestEvaluateExternalReachesNothing(t *testing.T) {
	p := everything("vendors")
	p.External = true
	r := Evaluate(Subject{Persona: p}, inv())
	assert.Equal(t, AccountExternal, r.AccountType)
	assert.Equal(t, 0, r.Tools.Callable)
	assert.Equal(t, 4, r.Tools.Total)
	assert.Equal(t, DecisionExternal, r.Tools.Items[1].Decision)
	assert.Equal(t, 0, r.APIOperations.Callable)
	assert.Equal(t, OpExternal, r.APIOperations.Items[0].Decision)
	assert.Empty(t, r.Scratch)
	assert.NotNil(t, r.Scratch, "an empty list, never null")
	assert.False(t, r.Sharing)
	assert.Equal(t, []string{"user"}, r.ResourceScopes, "their own folder alone: no persona folder, no Global (#2290)")
	assert.Contains(t, r.Portal.CappedByExternal, "scratch_tables")
}

// A persona that can run trino_execute on a scratch connection can drop any
// registered table through SQL, and the page says so (#2257).
func TestEvaluateScratch(t *testing.T) {
	r := Evaluate(Subject{Persona: everything("engineer")}, inv())
	require.Len(t, r.Scratch, 1)
	s := r.Scratch[0]
	assert.Equal(t, "acme-trino", s.Connection)
	assert.True(t, s.List)
	assert.True(t, s.Register)
	assert.True(t, s.DropViaSQL)
	assert.Equal(t, 14, s.Registrations)
	assert.Equal(t, []string{"acme-trino", "warehouse"}, s.QueryableFrom)

	reader := everything("reader")
	reader.Tools = persona.ToolRules{Allow: []string{"trino_query"}}
	reader.Portal = &persona.PortalAccess{ScratchTables: persona.PortalNone}
	r = Evaluate(Subject{Persona: reader}, inv())
	require.Len(t, r.Scratch, 1, "reads every registered table through trino_query")
	assert.False(t, r.Scratch[0].DropViaSQL)
	assert.False(t, r.Scratch[0].List)
}

// A tool allowed by a pattern whose only connections are denied is not
// callable; a platform tool is callable by its rule alone (#2257).
func TestEvaluateToolsFollowTheirConnections(t *testing.T) {
	p := &persona.Persona{
		Name: "analyst", Roles: []string{"analyst"},
		Tools:       persona.ToolRules{Allow: []string{"trino_*", "search"}, Deny: []string{"trino_execute"}},
		Connections: persona.ConnectionRules{Allow: []string{"crm-*"}},
	}
	r := Evaluate(Subject{Persona: p}, inv())
	byName := map[string]ToolItem{}
	for _, it := range r.Tools.Items {
		byName[it.Name] = it
	}
	assert.True(t, byName["search"].Callable)
	q := byName["trino_query"]
	assert.Equal(t, DecisionAllow, q.Decision)
	assert.Equal(t, "trino_*", q.Matched)
	assert.False(t, q.Exact)
	assert.False(t, q.Callable, "allowed by pattern, every connection denied")
	assert.Len(t, q.Connections, 2)
	assert.Equal(t, DecisionDeny, byName["trino_execute"].Decision)
	assert.Equal(t, DecisionNone, byName["api_invoke_endpoint"].Decision)
	assert.Equal(t, 1, r.Tools.Callable)
	assert.Equal(t, 1, r.Connections.Allowed)
	assert.Equal(t, OpTool, r.APIOperations.Items[0].Decision, "api_invoke_endpoint is not allowed")
	assert.Equal(t, []string{"ops"}, r.Connections.Items[2].UsedByChannels)
}

// Operations name what decided them: the connection, a rule, or no rule.
func TestEvaluateOperations(t *testing.T) {
	p := everything("ops")
	p.APIRoutes = []persona.APIRouteRule{
		{Connection: "crm-api", Methods: []string{"GET"}, Action: persona.ActionAllow},
	}
	r := Evaluate(Subject{Persona: p}, inv())
	assert.Equal(t, OpRuleAllow, r.APIOperations.Items[0].Decision)
	assert.Equal(t, 0, r.APIOperations.Items[0].RuleIndex)
	assert.True(t, r.APIOperations.Items[0].Callable)
	assert.Equal(t, OpRuleNoMatch, r.APIOperations.Items[1].Decision)
	assert.Equal(t, -1, r.APIOperations.Items[1].RuleIndex)

	p.APIRoutes = append(p.APIRoutes, persona.APIRouteRule{Connection: "crm-api", Methods: []string{"DELETE"}, Action: persona.ActionDeny})
	r = Evaluate(Subject{Persona: p}, inv())
	assert.Equal(t, OpRuleDeny, r.APIOperations.Items[1].Decision)
	assert.Equal(t, 1, r.APIOperations.Items[1].RuleIndex)

	open := everything("open")
	r = Evaluate(Subject{Persona: open}, inv())
	assert.Equal(t, OpOpen, r.APIOperations.Items[1].Decision)
	assert.Equal(t, 2, r.APIOperations.Callable)

	noConn := everything("noconn")
	noConn.Connections = persona.ConnectionRules{Allow: []string{"acme-*"}}
	r = Evaluate(Subject{Persona: noConn}, inv())
	assert.Equal(t, OpConnection, r.APIOperations.Items[0].Decision)
}

// Who gets the persona: its roles, the roles role_mapping assigns it, and
// the other personas that list one of its roles.
func TestEvaluateAssignment(t *testing.T) {
	p := everything("analyst")
	p.Roles = []string{"analyst", "staff"}
	p.ServiceAccount = true
	others := []*persona.Persona{p, {Name: "engineer", Roles: []string{"staff"}, Priority: 20}}
	r := Evaluate(Subject{
		Persona: p, Others: others,
		RoleMapping:       map[string]string{"oidc-analysts": "analyst", "oidc-eng": "engineer"},
		ExcludedFromCalls: []string{"analyst"},
	}, inv())
	assert.Equal(t, AccountAutomated, r.AccountType)
	assert.True(t, r.CallsExcludedByConfig)
	assert.Equal(t, []string{"oidc-analysts"}, r.Assignment.RoleMapping)
	assert.Equal(t, "persona-admin:analyst", r.Assignment.FolderWriterRole)
	assert.Equal(t, []RoleOverlap{{Role: "staff", Persona: "engineer", Priority: 20}}, r.Assignment.RoleOverlaps)
}

// The administrator has every portal area and every folder.
func TestEvaluateAdministrator(t *testing.T) {
	p := everything("admin")
	off := false
	p.Portal = &persona.PortalAccess{Knowledge: persona.PortalNone, Download: &off}
	r := Evaluate(Subject{Persona: p, Admin: true}, inv())
	assert.True(t, r.IsAdmin)
	assert.Equal(t, persona.PortalWrite, r.Portal.Effective.Knowledge)
	assert.Equal(t, persona.PortalNone, r.Portal.Stored.Knowledge)
	assert.True(t, r.Sharing)
	assert.Equal(t, []string{"user", "persona:*", "global"}, r.ResourceScopes)
}

func TestResourceScopes(t *testing.T) {
	assert.Equal(t, []string{"user", "global"}, resourceScopes("p", persona.PortalOwn, false, false))
	assert.Equal(t, []string{}, resourceScopes("p", persona.PortalNone, false, false))
	assert.Equal(t, []string{"user", "persona:p", "global"}, resourceScopes("p", persona.PortalPersonaFolders, false, false))
	assert.Equal(t, []string{"user"}, resourceScopes("p", persona.PortalShared, true, false), "external: their own folder")
	assert.Equal(t, []string{}, resourceScopes("p", persona.PortalNone, true, false), "external at none: nothing")
}

func TestReferencesEmpty(t *testing.T) {
	assert.True(t, emptyRefs().Empty())
	assert.True(t, References{Secrets: SecretRefs{Unrestricted: 3}}.Empty(), "a secret naming no persona is not a reference to this one")
	for _, r := range []References{
		{Secrets: SecretRefs{Named: []string{"crm-token"}}},
		{ScriptGrants: 1},
		{ScriptVersions: 1},
		{Prompts: 1},
		{Shares: 1},
		{WebhookSources: []string{"stripe"}},
		{People: []string{"ann@example.com"}},
		{APIKeys: 1},
	} {
		assert.False(t, r.Empty(), "%+v", r)
	}
}
