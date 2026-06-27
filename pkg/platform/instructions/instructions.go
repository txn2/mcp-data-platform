// Package instructions owns the agent-facing instruction text the platform
// presents through platform_info: the platform-owned "how to operate" baseline
// (#646), the full instruction composition (baseline beneath the admin business
// context, persona tuning, and runtime notes), and the platform_info tool's own
// title and description. Concentrating this text and its layering rules in one
// package keeps it out of the pkg/platform orchestration code and lets the
// admin baseline endpoint render the same baseline the agent receives.
package instructions

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Baseline tool names. The baseline names a tool only when that tool is actually
// available to the caller, so these are the MCP tool names its fragments
// reference, kept here so the baseline text and the gate that includes it cannot
// drift apart.
const (
	toolSearch         = "search"
	toolMemoryCapture  = "memory_capture"
	toolApplyKnowledge = "apply_knowledge"
)

// Build returns the platform-owned "how to operate this platform" instruction
// baseline (#646): the universal operating model that is true for every
// deployment (discover before acting, reuse what is known, capture what you
// learn). It is composed beneath the admin-configured business context
// (server.agent_instructions) rather than re-authored per deployment, and it is
// versioned with the binary so upgrading the platform updates it everywhere with
// no per-deployment edits.
//
// It names a tool only when that tool is in accessibleTools, so the baseline
// never tells an agent to call a tool its persona cannot reach or that the
// deployment did not enable. accessibleTools is the set of tool names available
// to the caller: registered on the platform and, for a per-caller baseline,
// allowed by the caller's persona. A set with none of the baseline's tools
// yields an empty baseline, since there is nothing to say without a tool to name.
func Build(accessibleTools []string) string {
	has := make(map[string]bool, len(accessibleTools))
	for _, t := range accessibleTools {
		has[t] = true
	}

	var bullets []string
	if has[toolSearch] {
		bullets = append(bullets,
			"Discover before you act. Call `search` first: one query reveals what is already "+
				"known across every source you can reach (the data catalog, its context documents, your memory, captured "+
				"insights, knowledge pages, your feedback, prompts, API endpoints, and connections). "+
				"The answer may span several sources, or may not be in the data warehouse at all, so do "+
				"not assume a backend and do not stop at the first result.",
			"Reuse what is known. Treat `search` results as the starting point and drill in with "+
				"the scoped tool a result points to, rather than re-deriving an answer or re-asking "+
				"the user for something already recorded.")
	}
	if has[toolMemoryCapture] {
		bullets = append(bullets,
			"Capture what you learn. When you establish something durable (a definition, a "+
				"correction, a data-quality finding), record it with `memory_capture` so it is "+
				"available next time instead of rediscovered.")
	}
	if has[toolApplyKnowledge] {
		bullets = append(bullets,
			"Synthesize durable knowledge. Captured insights enter a review queue you drive with "+
				"`apply_knowledge`: list it via action `bulk_review` with `itemize:true`, then promote each "+
				"insight to a DataHub catalog entity when the fact is tied to a specific dataset (a `urn:li:...` "+
				"reference) or to a canonical knowledge page when it is broader business or domain knowledge "+
				"(an `mcp:<type>:<key>` reference). These are two distinct namespaces: cite an entity from a page "+
				"with the `reference` string that search results and `list_connections` carry, and never cross the "+
				"two schemes (no `urn:li:mcp:...`). To make a citation a tracked, clickable reference, write it in "+
				"plain text or a markdown link in the page body, or pass it in the page's `references` list; a "+
				"reference inside backticks or a code block is treated as an example and ignored.")
	}
	if len(bullets) == 0 {
		return ""
	}

	lines := make([]string, 0, len(bullets)+1)
	lines = append(lines, "How to operate this platform:")
	for _, bullet := range bullets {
		lines = append(lines, "- "+bullet)
	}
	return strings.Join(lines, "\n")
}

// Compose joins the platform baseline above the rest of the instruction stack
// (admin business context + persona tuning + runtime notes). The baseline is
// always first and is never overridden by the admin or persona layers; either
// side may be empty.
func Compose(baseline, rest string) string {
	baseline = strings.TrimSpace(baseline)
	rest = strings.TrimSpace(rest)
	switch {
	case baseline == "":
		return rest
	case rest == "":
		return baseline
	default:
		return baseline + "\n\n" + rest
	}
}

// AccessibleTools narrows allTools to the names the caller's persona may call,
// so the baseline names only reachable tools. A nil persona means no persona
// filtering is in effect (the caller can reach every registered tool); a
// resolved persona is filtered fail-closed by its allow/deny rules.
func AccessibleTools(allTools []string, p *persona.Persona, reg *persona.Registry) []string {
	if p == nil {
		return allTools
	}
	return persona.NewToolFilter(reg).FilterTools(p, allTools)
}

// ComposeForCaller assembles the full agent-instruction stack one caller sees in
// platform_info, layering it in a fixed order so the rule lives in one place:
//
//  1. the platform baseline (gated to the tools this persona may call),
//  2. the admin business/deployment context, with the persona's
//     suffix/override applied to that layer only,
//  3. runtime notes (for example the uploaded-resources hint), appended last.
//
// The baseline is always present and is never replaced by the admin or persona
// layers. p may be nil (no persona filtering or tuning). Blank notes are
// skipped.
func ComposeForCaller(adminLayer string, allTools []string, p *persona.Persona, reg *persona.Registry, notes ...string) string {
	if p != nil {
		adminLayer = p.ApplyAgentInstructions(adminLayer)
	}
	out := Compose(Build(AccessibleTools(allTools, p, reg)), adminLayer)
	for _, note := range notes {
		out = Compose(out, note)
	}
	return out
}

// InfoToolTitle returns the display name for the platform_info tool. A custom
// server name is used as the title (so a client shows e.g. "ACME Data Platform"
// instead of "platform_info"); the default server name falls back to fallback.
func InfoToolTitle(serverName, defaultServerName, fallback string) string {
	if serverName != "" && serverName != defaultServerName {
		return serverName
	}
	return fallback
}

// InfoToolDescription builds the platform_info tool's description. It is itself
// operating-model text (platform_info is the mandatory first call, then search),
// so it lives here next to the baseline that carries the same guidance. A custom
// server name and tags are woven in for discovery.
func InfoToolDescription(serverName, defaultServerName string, tags []string) string {
	base := "MANDATORY first call in every session. "
	if serverName != "" && serverName != defaultServerName {
		base += fmt.Sprintf("Get information about %s", serverName)
	} else {
		base += "Get information about this MCP data platform"
	}
	if len(tags) > 0 {
		base += fmt.Sprintf(" (%s)", strings.Join(tags, ", "))
	}
	return base + ", including its purpose, available toolkits, and enabled features. " +
		"This tool MUST be called before any other tool (search, trino_query, " +
		"trino_describe_table, s3_list_objects, etc.). Then call search, the one way to " +
		"discover, to reuse what is already known before re-asking the user or re-deriving it. " +
		"Skipping these causes incorrect query routing, operational rule violations, and degraded output quality."
}
