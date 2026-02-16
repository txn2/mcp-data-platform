package platform

import (
	"log/slog"
	"regexp"
	"strings"
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
func (p *Platform) validateAgentInstructions() {
	instructions := p.config.Server.AgentInstructions
	if instructions == "" {
		return
	}

	registeredTools := p.toolkitRegistry.AllTools()
	// Add platform-level tools that are registered outside the toolkit registry.
	registeredTools = append(registeredTools, "platform_info")

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

// hasKnownPrefix reports whether the token starts with a known tool prefix.
func hasKnownPrefix(token string) bool {
	for _, prefix := range knownToolPrefixes {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}
