package platform

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/toolnames"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// validateAgentInstructions scans the deployment's customized agent-instruction
// layer for tool-name-like tokens this deployment does not register and logs a
// warning for each, so a stale reference left by a tool rename or removal is
// visible rather than silently instructing an agent to call nothing.
//
// The token set is derived from the deployment's own registered names
// (toolnames.Unknown), so it covers every tool the platform
// actually exposes.
//
// Startup-only, so it lints the value in force at boot. An override authored
// later is not re-linted here; the apply_knowledge agent_instructions sink runs
// the same check as a write-time refusal on its own promotions (#1607).
func (p *Platform) validateAgentInstructions() {
	text := p.config.ServerAgentInstructions(context.Background())
	if text == "" {
		return
	}
	registered := RegisteredToolNames(p.toolkitRegistry.AllTools(), p.PlatformTools())
	for _, token := range toolnames.Unknown(text, registered) {
		slog.Warn("agent_instructions references unrecognized tool",
			"token", token,
			"hint", "verify the tool name exists or remove the stale reference",
		)
	}
}

// validatePersonaCoherence warns for every persona that holds a capability it
// cannot complete — search without fetch, memory_capture or apply_knowledge
// without search (see persona.CheckCoherence for the rule table).
//
// This is deliberately not a gate. An operator may have a real reason for a
// restricted persona, so a finding logs and startup continues. It exists
// because the degradation is otherwise invisible: the instruction baseline
// omits guidance for a tool the caller cannot reach (instructions.reuseBullet),
// which is correct — never instruct an unreachable tool — but it converts a
// misconfiguration into silent capability loss with no error, no warning, and
// no symptom beyond an unauthorized audit row if an agent guesses the tool name
// unprompted.
//
// Startup-only for the file/DB personas in force at boot; a persona authored
// later is checked on write by the admin API.
func (p *Platform) validatePersonaCoherence() {
	registered := p.toolkitRegistry.AllTools()
	for _, f := range persona.CheckRegistryCoherence(p.personaRegistry, registered) {
		slog.Warn("persona grants a capability it cannot complete",
			"persona", logsan.SanitizeForLog(f.Persona),
			"granted", f.Granted,
			"missing", f.Missing,
			"why", f.Why,
			"remedy", logsan.SanitizeForLog(f.Remedy),
		)
	}
}
