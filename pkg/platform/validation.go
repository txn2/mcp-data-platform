package platform

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// knownToolPrefixes lists the prefixes that identify tool-name-like tokens
// in agent_instructions text. These match the naming conventions of registered
// toolkits (e.g., "trino_query", "datahub_search", "s3_list_buckets").
var knownToolPrefixes = []string{
	"trino_",
	"datahub_",
	"s3_",
	"platform_",
	"capture_",
	"apply_",
}

// toolTokenPattern matches word-boundary tokens that look like tool names:
// a known prefix followed by one or more lowercase letters/digits/underscores.
var toolTokenPattern = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\b`)

// validateAgentInstructions scans the agent_instructions text for tokens that
// look like tool names and logs warnings for any that don't match registered tools.
// This helps catch stale references after tool renames or removals.
//
// Startup-only, so it lints the value in force at boot. An override authored
// later is not re-linted; the warning is a developer aid, not a gate.
func (p *Platform) validateAgentInstructions() {
	instructions := p.config.ServerAgentInstructions(context.Background())
	if instructions == "" {
		return
	}

	registeredTools := RegisteredToolNames(p.toolkitRegistry.AllTools(), p.PlatformTools())

	toolSet := make(map[string]struct{}, len(registeredTools))
	for _, t := range registeredTools {
		toolSet[t] = struct{}{}
	}

	tokens := toolTokenPattern.FindAllString(instructions, -1)
	for _, token := range tokens {
		if !hasKnownPrefix(token) {
			continue
		}
		if _, ok := toolSet[token]; !ok {
			slog.Warn("agent_instructions references unrecognized tool",
				"token", token,
				"hint", "verify the tool name exists or remove the stale reference",
			)
		}
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

// hasKnownPrefix reports whether the token starts with a known tool prefix.
func hasKnownPrefix(token string) bool {
	for _, prefix := range knownToolPrefixes {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}
