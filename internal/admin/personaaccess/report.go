// Package personaaccess answers what a persona, saved or not, lets its people
// reach (#2257): the tools they can call and on which connections, the API
// operations, the scratch tables, the portal, sharing and the resource
// folders, the grants other objects hold that name the persona, and what no
// persona setting governs. The persona editor shows it beside every unsaved
// change, and the persona delete lists its references from it.
//
// Every answer is the evaluation a request meets: persona.ToolFilter for tools,
// connections and API routes, the portal levels for the portal, and
// Registry.AdminRule for the administrator. Nothing here decides access; it
// reports what the deciders would decide.
//
//nolint:revive // max-public-structs: one cohesive report (the draft a request carries, the report and its tool, operation, table, portal, sharing and folder sections, and the references naming the persona), not a heap of unrelated types.
package personaaccess

import (
	"slices"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Account types a persona can be (#2257): one of them, stored as the
// persona's external and service_account flags.
const (
	AccountInternal  = "internal"
	AccountExternal  = "external"
	AccountAutomated = "automated"
)

// Tool decisions.
const (
	DecisionAllow    = "allow"
	DecisionDeny     = "deny"
	DecisionNone     = "none"
	DecisionExternal = "external"
)

// API operation decisions: what decided whether api_invoke_endpoint may call
// it. open is a connection no route rule touches.
const (
	OpOpen        = "open"
	OpRuleAllow   = "rule_allow"
	OpRuleDeny    = "rule_deny"
	OpRuleNoMatch = "rule_nomatch"
	OpConnection  = "connection"
	OpTool        = "tool"
	OpExternal    = "external"
)

// The tools a scratch table is changed and read through.
const (
	toolExecute = "trino_execute"
	toolQuery   = "trino_query"
	toolInvoke  = "api_invoke_endpoint"
	kindTrino   = "trino"
)

// personaAdminPrefix is the role that writes a persona's folder
// (pkg/resource's persona-admin grant).
const personaAdminPrefix = "persona-admin:"

// Tool is one tool the deployment registers. Kind is its toolkit's kind, empty
// for a platform tool that reaches no connection. Connection is set when the
// tool is bound to one connection rather than taking one per call.
type Tool struct {
	Name       string
	Kind       string
	Connection string
}

// Scratch is a Trino connection's scratch catalog (docs/server/scratch-catalog.md).
type Scratch struct {
	Catalog string `json:"catalog"`
	Schema  string `json:"schema"`
	// Writes reports whether the connection accepts writes, which registering
	// a table and changing one through SQL both need.
	Writes bool `json:"-"`
}

// Connection is one connection the deployment serves.
type Connection struct {
	Name     string
	Kind     string
	Scratch  *Scratch
	Channels []string
}

// Operation is one operation an api connection's catalog declares.
type Operation struct {
	Connection string
	Method     string
	Path       string
}

// Inventory is what the deployment serves, read per request so a connection
// added at run time is in the next answer.
type Inventory struct {
	Tools       []Tool
	Connections []Connection
	Operations  []Operation
	// Registrations counts the scratch tables registered on each connection.
	Registrations map[string]int
}

// Report is the answer.
type Report struct {
	IsAdmin bool `json:"is_admin"`
	// AdminPersona is the configured administrator persona's name, which is
	// never external.
	AdminPersona          string        `json:"admin_persona"`
	AccountType           string        `json:"account_type"`
	CallsExcludedByConfig bool          `json:"calls_excluded_by_config"`
	Assignment            Assignment    `json:"assignment"`
	Tools                 ToolSummary   `json:"tools"`
	Connections           ConnSummary   `json:"connections"`
	APIOperations         OpSummary     `json:"api_operations"`
	Scratch               []ScratchItem `json:"scratch"`
	Portal                PortalSummary `json:"portal"`
	Sharing               bool          `json:"sharing"`
	ResourceScopes        []string      `json:"resource_scopes"`
	References            References    `json:"references"`
	Unscoped              Unscoped      `json:"unscoped"`
}

// Assignment is who gets the persona and who reaches its folder.
type Assignment struct {
	Roles []string `json:"roles"`
	// RoleMapping are the roles personas.role_mapping assigns to this
	// persona outright, before roles and priority are compared.
	RoleMapping []string `json:"role_mapping"`
	// FolderReaderRoles make a person a member of the persona's folder and
	// prompts: any of them, whatever persona the person resolves to.
	FolderReaderRoles []string `json:"folder_reader_roles"`
	// FolderWriterRole writes the persona's folder; administrators do too.
	FolderWriterRole string        `json:"folder_writer_role"`
	RoleOverlaps     []RoleOverlap `json:"role_overlaps"`
}

// RoleOverlap is another persona listing one of this persona's roles.
type RoleOverlap struct {
	Role     string `json:"role"`
	Persona  string `json:"persona"`
	Priority int    `json:"priority"`
}

// ToolSummary is the tools a persona's people can call.
type ToolSummary struct {
	Total    int        `json:"total"`
	Callable int        `json:"callable"`
	Items    []ToolItem `json:"items"`
}

// ToolItem is one tool's decision.
type ToolItem struct {
	Name        string       `json:"name"`
	Kind        string       `json:"kind,omitempty"`
	Decision    string       `json:"decision"`
	Matched     string       `json:"matched,omitempty"`
	Exact       bool         `json:"exact"`
	Connections []ConnAccess `json:"connections"`
	Callable    bool         `json:"callable"`
}

// ConnAccess is one connection a tool reaches and whether the persona does.
type ConnAccess struct {
	Name    string `json:"name"`
	Allowed bool   `json:"allowed"`
}

// ConnSummary is the connections a persona's tool calls reach.
type ConnSummary struct {
	Total   int        `json:"total"`
	Allowed int        `json:"allowed"`
	Items   []ConnItem `json:"items"`
}

// ConnItem is one connection's decision.
type ConnItem struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Decision       string   `json:"decision"`
	Matched        string   `json:"matched,omitempty"`
	Exact          bool     `json:"exact"`
	Scratch        *Scratch `json:"scratch,omitempty"`
	UsedByChannels []string `json:"used_by_channels"`
}

// OpSummary is the API operations api_invoke_endpoint may call.
type OpSummary struct {
	Total    int      `json:"total"`
	Callable int      `json:"callable"`
	Items    []OpItem `json:"items"`
}

// OpItem is one operation's decision. RuleIndex is the persona's API route
// rule that decided it, -1 for none.
type OpItem struct {
	Connection string `json:"connection"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Decision   string `json:"decision"`
	RuleIndex  int    `json:"rule_index"`
	Callable   bool   `json:"callable"`
}

// ScratchItem is what the persona's people can do with the scratch tables on
// one connection they reach.
type ScratchItem struct {
	Connection string `json:"connection"`
	Catalog    string `json:"catalog"`
	Schema     string `json:"schema"`
	// List and Register are the portal's scratch-table routes.
	List     bool `json:"list"`
	Register bool `json:"register"`
	// DropViaSQL is trino_execute on the connection, whose Trino user holds
	// every privilege on the scratch catalog: any registered table can be
	// dropped or replaced, whoever registered it.
	DropViaSQL    bool `json:"drop_via_sql"`
	Registrations int  `json:"registrations"`
	// QueryableFrom are the connections trino_query reaches that read every
	// registered table when their Trino user reads the scratch catalog.
	QueryableFrom []string `json:"queryable_from"`
}

// PortalSummary is the portal block as stored, as its people get it, and
// what External lowered.
type PortalSummary struct {
	Stored           persona.PortalAccess `json:"stored"`
	Effective        persona.PortalAccess `json:"effective"`
	CappedByExternal []string             `json:"capped_by_external"`
}

// SecretRefs are the stored secrets that name the persona, and how many name
// no persona, which every persona may use.
type SecretRefs struct {
	Named        []string `json:"named"`
	Unrestricted int      `json:"unrestricted"`
}

// References are the grants other objects hold that name the persona. A
// persona deleted with them leaves them in place, and one created later under
// the same name inherits them.
type References struct {
	Secrets          SecretRefs `json:"secrets"`
	ScriptGrants     int        `json:"script_grants"`
	ScriptVersions   int        `json:"script_versions"`
	Prompts          int        `json:"prompts"`
	Shares           int        `json:"shares"`
	WebhookSources   []string   `json:"webhook_sources"`
	ExternalAccounts int        `json:"external_accounts"`
	APIKeys          int        `json:"api_keys"`
}

// Empty reports whether nothing names the persona. Secrets that name no
// persona are not a reference to this one.
func (r References) Empty() bool {
	return len(r.Secrets.Named) == 0 && r.ScriptGrants == 0 && r.ScriptVersions == 0 && r.Prompts == 0 &&
		r.Shares == 0 && len(r.WebhookSources) == 0 && r.ExternalAccounts == 0 && r.APIKeys == 0
}

// Unscoped is access no persona setting governs.
type Unscoped struct {
	// SignedInEndpoints admit any signed-in account, external included.
	SignedInEndpoints int `json:"signed_in_endpoints"`
}

// Subject is the persona being evaluated with what decides it.
type Subject struct {
	Persona *persona.Persona
	// Admin reports the persona is the administrator persona.
	Admin bool
	// Others are the other registered personas, for role overlaps.
	Others []*persona.Persona
	// RoleMapping is personas.role_mapping.oidc_to_persona.
	RoleMapping map[string]string
	// ExcludedFromCalls is calls.exclude_personas.
	ExcludedFromCalls []string
}

// Evaluate reports what the subject's persona reaches in inv. References and
// Unscoped are left for the caller, which reads them from the stores.
func Evaluate(s Subject, inv Inventory) Report {
	p := s.Persona
	f := persona.NewToolFilter(nil)
	conns := evaluateConnections(f, p, inv.Connections)
	tools := evaluateTools(f, p, inv.Tools, inv.Connections)
	granted := grantedTools(f, p, inv.Tools)
	stored, effective := p.StoredPortal(granted), p.PortalPermissions(granted)
	if s.Admin {
		effective = persona.FullPortalAccess()
	}
	r := Report{
		IsAdmin:               s.Admin,
		AccountType:           accountType(p),
		CallsExcludedByConfig: slices.Contains(s.ExcludedFromCalls, p.Name),
		Assignment:            assignment(s),
		Tools:                 tools,
		Connections:           conns,
		APIOperations:         evaluateOperations(f, p, tools, inv.Operations),
		Portal: PortalSummary{
			Stored: stored, Effective: effective, CappedByExternal: nonNil(persona.Differs(stored, effective)),
		},
		Sharing:        s.Admin || p.SharingAllowed(),
		ResourceScopes: resourceScopes(p.Name, effective.Resources, p.External, s.Admin),
	}
	r.Scratch = evaluateScratch(f, p, effective, tools, inv)
	return r
}

func accountType(p *persona.Persona) string {
	switch {
	case p.External:
		return AccountExternal
	case p.ServiceAccount:
		return AccountAutomated
	}
	return AccountInternal
}

func assignment(s Subject) Assignment {
	p := s.Persona
	a := Assignment{
		Roles:             nonNil(slices.Clone(p.Roles)),
		RoleMapping:       []string{},
		FolderReaderRoles: nonNil(slices.Clone(p.Roles)),
		FolderWriterRole:  personaAdminPrefix + p.Name,
		RoleOverlaps:      []RoleOverlap{},
	}
	for role, name := range s.RoleMapping {
		if name == p.Name {
			a.RoleMapping = append(a.RoleMapping, role)
		}
	}
	sort.Strings(a.RoleMapping)
	for _, other := range s.Others {
		if other.Name == p.Name {
			continue
		}
		for _, role := range p.Roles {
			if slices.Contains(other.Roles, role) {
				a.RoleOverlaps = append(a.RoleOverlaps, RoleOverlap{Role: role, Persona: other.Name, Priority: other.Priority})
			}
		}
	}
	sort.Slice(a.RoleOverlaps, func(i, j int) bool {
		if a.RoleOverlaps[i].Role != a.RoleOverlaps[j].Role {
			return a.RoleOverlaps[i].Role < a.RoleOverlaps[j].Role
		}
		return a.RoleOverlaps[i].Persona < a.RoleOverlaps[j].Persona
	})
	return a
}

func evaluateConnections(f *persona.ToolFilter, p *persona.Persona, conns []Connection) ConnSummary {
	out := ConnSummary{Total: len(conns), Items: make([]ConnItem, 0, len(conns))}
	for _, c := range conns {
		d := f.WhyConnectionAllowed(p, c.Name)
		item := ConnItem{
			Name: c.Name, Kind: c.Kind, Decision: decision(d),
			Matched: d.MatchedPattern, Exact: d.MatchedPattern == c.Name,
			Scratch: c.Scratch, UsedByChannels: nonNil(c.Channels),
		}
		if d.Allowed {
			out.Allowed++
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// decision names an access decision as the editor shows it.
func decision(d persona.AccessDecision) string {
	switch d.Source {
	case persona.AccessSourceAllow:
		return DecisionAllow
	case persona.AccessSourceDeny:
		return DecisionDeny
	}
	return DecisionNone
}

// toolConnections are the connections a tool reaches: the one it is bound to,
// or every connection of its kind, since it takes one per call.
func toolConnections(t Tool, conns []Connection) []string {
	if t.Connection != "" {
		return []string{t.Connection}
	}
	var out []string
	for _, c := range conns {
		if t.Kind != "" && c.Kind == t.Kind {
			out = append(out, c.Name)
		}
	}
	return out
}

func evaluateTools(f *persona.ToolFilter, p *persona.Persona, tools []Tool, conns []Connection) ToolSummary {
	out := ToolSummary{Total: len(tools), Items: make([]ToolItem, 0, len(tools))}
	for _, t := range tools {
		d := f.WhyAllowed(p, t.Name)
		item := ToolItem{
			Name: t.Name, Kind: t.Kind, Decision: decision(d),
			Matched: d.MatchedPattern, Exact: d.MatchedPattern == t.Name, Connections: []ConnAccess{},
		}
		reached := false
		names := toolConnections(t, conns)
		for _, name := range names {
			ok := f.IsConnectionAllowed(p, name)
			reached = reached || ok
			item.Connections = append(item.Connections, ConnAccess{Name: name, Allowed: ok})
		}
		if p.External {
			item.Decision = DecisionExternal
		}
		// A tool that reaches connections is callable on one the persona
		// reaches; a platform tool by its rule alone.
		item.Callable = d.Allowed && (len(names) == 0 || reached)
		if item.Callable {
			out.Callable++
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// grantedTools are the tools the persona's rules allow, which the portal's
// unset areas resolve from.
func grantedTools(f *persona.ToolFilter, p *persona.Persona, tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return f.FilterTools(p, names)
}

func callable(tools ToolSummary, name string) bool {
	for _, t := range tools.Items {
		if t.Name == name {
			return t.Callable
		}
	}
	return false
}

func evaluateOperations(f *persona.ToolFilter, p *persona.Persona, tools ToolSummary, ops []Operation) OpSummary {
	out := OpSummary{Total: len(ops), Items: make([]OpItem, 0, len(ops))}
	invoke := callable(tools, toolInvoke)
	for _, op := range ops {
		item := OpItem{Connection: op.Connection, Method: op.Method, Path: op.Path, RuleIndex: -1}
		d := f.WhyAPIRouteAllowed(p, persona.RouteQuery{Connection: op.Connection, Method: op.Method, Path: op.Path, Template: op.Path})
		if d.MatchedRule != nil {
			item.RuleIndex = ruleIndex(p.APIRoutes, *d.MatchedRule)
		}
		switch {
		case p.External:
			item.Decision = OpExternal
		case !f.IsAllowed(p, toolInvoke):
			item.Decision = OpTool
		case !f.IsConnectionAllowed(p, op.Connection):
			item.Decision = OpConnection
		default:
			item.Decision = routeDecision(d)
			item.Callable = d.Allowed && invoke
		}
		if item.Callable {
			out.Callable++
		}
		out.Items = append(out.Items, item)
	}
	return out
}

func routeDecision(d persona.RouteDecision) string {
	switch {
	case d.Source == persona.AccessSourceAllow:
		return OpRuleAllow
	case d.Source == persona.AccessSourceDeny:
		return OpRuleDeny
	case d.Allowed:
		return OpOpen
	}
	return OpRuleNoMatch
}

func ruleIndex(rules []persona.APIRouteRule, rule persona.APIRouteRule) int {
	for i, r := range rules {
		if r.Connection == rule.Connection && r.Action == rule.Action &&
			slices.Equal(r.Methods, rule.Methods) && slices.Equal(r.Paths, rule.Paths) {
			return i
		}
	}
	return -1
}

func evaluateScratch(f *persona.ToolFilter, p *persona.Persona, eff persona.PortalAccess, tools ToolSummary, inv Inventory) []ScratchItem {
	out := []ScratchItem{}
	queryVia := queryConnections(f, p, tools, inv)
	execute := f.IsAllowed(p, toolExecute)
	for _, c := range inv.Connections {
		if c.Scratch == nil || !f.IsConnectionAllowed(p, c.Name) {
			continue
		}
		item := ScratchItem{
			Connection: c.Name, Catalog: c.Scratch.Catalog, Schema: c.Scratch.Schema,
			List:          persona.AtLeast(eff.ScratchTables, persona.PortalList),
			Register:      persona.AtLeast(eff.ScratchTables, persona.PortalRegister) && c.Scratch.Writes,
			DropViaSQL:    execute && c.Scratch.Writes,
			Registrations: inv.Registrations[c.Name],
			QueryableFrom: queryVia,
		}
		if item.List || item.Register || item.DropViaSQL || len(item.QueryableFrom) > 0 {
			out = append(out, item)
		}
	}
	return out
}

// queryConnections are the Trino connections the persona can query a scratch
// table through: none without the query tool.
func queryConnections(f *persona.ToolFilter, p *persona.Persona, tools ToolSummary, inv Inventory) []string {
	out := []string{}
	if !callable(tools, toolQuery) {
		return out
	}
	for _, c := range inv.Connections {
		if c.Kind == kindTrino && f.IsConnectionAllowed(p, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

// resourceScopes are the resource folders the persona's people read, as the
// resources API decides them (internal/httpserver/resourcelevel).
func resourceScopes(name, level string, external, admin bool) []string {
	const user, global = "user", "global"
	if admin {
		return []string{user, "persona:*", global}
	}
	var out []string
	switch level {
	case persona.PortalShared, persona.PortalPersonaFolders:
		out = []string{user, "persona:" + name, global}
	case persona.PortalOwn:
		out = []string{user, global}
	}
	if external {
		out = slices.DeleteFunc(out, func(s string) bool { return s == global })
	}
	return nonNil(out)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
