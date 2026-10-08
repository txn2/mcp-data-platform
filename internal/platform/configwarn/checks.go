package configwarn

import (
	"context"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/toolnames"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Connection names one configured connection.
type Connection struct {
	Kind string
	Name string
}

// Startup is what the boot checks read.
type Startup struct {
	// AgentInstructions is the customized agent-instruction layer in force.
	AgentInstructions string
	// Registered is every tool name the deployment registers, the platform's
	// own tools included.
	Registered []string
	// Toolkits is the toolkit registry: the coherence rules are keyed to the
	// tools its toolkits register, and its connections are checked for reach.
	Toolkits *registry.Registry
	// Personas is the persona registry in force.
	Personas *persona.Registry
}

// CheckStartup runs every boot check, logging and counting each finding.
// Startup-only for the values in force at boot; a persona written later is
// checked by CheckPersona from the admin API.
func CheckStartup(ctx context.Context, s Startup) {
	checkAgentInstructions(ctx, s.AgentInstructions, s.Registered)
	if s.Personas == nil {
		return
	}
	personas := s.Personas.All()
	slices.SortFunc(personas, func(a, b *persona.Persona) int { return strings.Compare(a.Name, b.Name) })
	for _, p := range personas {
		checkPersonaTools(ctx, p, s.Registered)
	}
	var toolkitTools []string
	if s.Toolkits != nil {
		toolkitTools = s.Toolkits.AllTools()
	}
	for _, f := range persona.CheckRegistryCoherence(s.Personas, toolkitTools) {
		warnIncoherent(ctx, f)
	}
	checkUnusedConnections(ctx, s.Personas, personas, Connections(s.Toolkits))
}

// Connections is every connection the registry's toolkits serve, as the
// toolkits that list their connections report them, sorted by kind and name.
func Connections(reg *registry.Registry) []Connection {
	if reg == nil {
		return nil
	}
	var out []Connection
	for _, tk := range reg.All() {
		lister, ok := tk.(toolkit.ConnectionLister)
		if !ok {
			continue
		}
		for _, c := range lister.ListConnections() {
			out = append(out, Connection{Kind: tk.Kind(), Name: c.Name})
		}
	}
	slices.SortFunc(out, func(a, b Connection) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return slices.Compact(out)
}

// CheckPersona runs the persona checks for one persona written through the
// admin API, so the warning a boot would raise is raised at write time.
func CheckPersona(ctx context.Context, p *persona.Persona, registered, toolkitTools []string) {
	if p == nil {
		return
	}
	checkPersonaTools(ctx, p, registered)
	for _, f := range persona.CheckCoherence(p, toolkitTools) {
		warnIncoherent(ctx, f)
	}
}

// checkAgentInstructions warns for every tool-name-like token in the agent
// instructions that this deployment does not register, so a stale reference
// left by a rename is visible rather than instructing an agent to call
// nothing. The token set is derived from the registered names (toolnames).
func checkAgentInstructions(ctx context.Context, text string, registered []string) {
	if text == "" {
		return
	}
	for _, token := range toolnames.Unknown(text, registered) {
		Warn(ctx, CodeAgentInstructionsUnknownTool, "agent_instructions references unrecognized tool",
			"token", token,
			"hint", "verify the tool name exists or remove the stale reference",
		)
	}
}

// checkPersonaTools warns for every tools.allow entry with no wildcard that
// names a tool the deployment does not register. Such an entry grants
// nothing, and it usually means a tool was renamed or its toolkit is not
// configured here; a pattern is left alone, since matching nothing today is
// what a pattern for a toolkit added later does.
func checkPersonaTools(ctx context.Context, p *persona.Persona, registered []string) {
	for _, entry := range p.Tools.Allow {
		if strings.ContainsAny(entry, `*?[\`) || slices.Contains(registered, entry) {
			continue
		}
		Warn(ctx, CodePersonaToolUnregistered, "persona allows a tool this deployment does not register",
			"persona", logsan.SanitizeForLog(p.Name),
			"tool", logsan.SanitizeForLog(entry),
			"hint", "correct the tool name, or configure the toolkit that registers it",
		)
	}
}

// warnIncoherent logs one coherence finding: a persona that holds a
// capability it cannot complete (persona.CheckCoherence's rule table). It is
// advisory; an operator may mean a restricted persona.
func warnIncoherent(ctx context.Context, f persona.CoherenceFinding) {
	Warn(ctx, CodePersonaIncoherent, "persona grants a capability it cannot complete",
		"persona", logsan.SanitizeForLog(f.Persona),
		"granted", f.Granted,
		"missing", f.Missing,
		"why", f.Why,
		"remedy", logsan.SanitizeForLog(f.Remedy),
	)
}

// checkUnusedConnections warns for every connection no persona may reach.
// Connections are deny-by-default, so such a connection is served and never
// callable: usually a persona's connections block that was not updated when
// the connection was added.
func checkUnusedConnections(ctx context.Context, reg *persona.Registry, personas []*persona.Persona, conns []Connection) {
	filter := persona.NewToolFilter(reg)
	for _, c := range conns {
		reachable := slices.ContainsFunc(personas, func(p *persona.Persona) bool {
			return filter.IsConnectionAllowed(p, c.Name)
		})
		if reachable {
			continue
		}
		Warn(ctx, CodeUnusedConnection, "no persona may reach a configured connection",
			"kind", logsan.SanitizeForLog(c.Kind),
			"connection", logsan.SanitizeForLog(c.Name),
			"hint", "add the connection to a persona's connections.allow, or remove it",
		)
	}
}
