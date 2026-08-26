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
	toolFetch          = "fetch"
	toolMemoryCapture  = "memory_capture"
	toolApplyKnowledge = "apply_knowledge"
	toolManagePrompt   = "manage_prompt"
	toolManageFeedback = "manage_feedback"
	toolTrinoQuery     = "trino_query"
	toolManageScript   = "manage_script"
	toolSaveAsset      = "save_asset"
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
	has := toolSet(accessibleTools)

	var bullets []string
	if has[toolSearch] {
		bullets = append(bullets,
			"Discover before you act. Call `search` first: one query reveals what is already "+
				"known across every source you can reach (the data catalog, its context documents, your memory, captured "+
				"insights, knowledge pages, your feedback, saved assets, uploaded reference material, prompts, "+
				"API endpoints, and connections). "+
				"The answer may span several sources, or may not be in the data warehouse at all, so do "+
				"not assume a backend and do not stop at the first result.",
			reuseBullet(has[toolFetch]))
	}
	if has[toolManagePrompt] {
		bullets = append(bullets,
			"Named procedures are prompts. When the user names a report, procedure, or recurring "+
				"task (\"run the daily sales report\"), resolve it against the prompt library first: "+
				"call `manage_prompt` with command `use` and the name as given. It accepts display "+
				"names, mcp:prompt:<id> references, and free text, returns the ready-to-run prompt "+
				"content with its arguments, and lists candidates when ambiguous, so resolve rather "+
				"than enumerate.")
	}
	if has[toolTrinoQuery] {
		bullets = append(bullets,
			"A short list of outside keys needs no table. When the user brings keys that are not "+
				"in the warehouse -- a pasted list of ids, a handful of codes -- join them inline: "+
				"`JOIN (VALUES ('a'),('b')) AS t(id)` or `WHERE id IN (...)` through `trino_query` on "+
				"a read-only connection. Do not ask for a table to be created and do not refuse; a "+
				"few thousand rows join this way. Above that, the file belongs in the platform: "+
				"upload it as a resource or save it as an asset, register it as a table, and join "+
				"the registered table by name.")
	}
	if has[toolSaveAsset] {
		bullets = append(bullets, referencesBullet(has[toolFetch]))
	}
	if has[toolManageScript] {
		bullets = append(bullets, scriptsBullet(has[toolFetch]))
	}
	if has[toolMemoryCapture] {
		bullets = append(bullets,
			"Capture what you learn. When you establish something durable (a definition, a "+
				"correction, a data-quality finding), record it with `memory_capture` so it is "+
				"available next time instead of rediscovered.",
			"Record the query that answered the question. Every query and API call returns its "+
				"own `call_id`. When a result met the purpose you stated and the statement is worth "+
				"running again (including when the answer only went into the conversation and was "+
				"never saved as an asset), name that id in `memory_capture` `sources` with a "+
				"description of what it answers and any caveats. That is what turns a one-off "+
				"statement into something the next person finds, and what puts it up for promotion "+
				"to the catalog.")
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
				"reference inside backticks or a code block is treated as an example and ignored. Prefer several "+
				"focused, cross-linked pages over one large page: cite related pages with `mcp:knowledge_page:` "+
				"references and build a thin index page that links to them. Creating a page that duplicates an "+
				"existing one is blocked with the candidates returned, so update in place rather than re-teaching a fact.")
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

// toolSet indexes the caller's accessible tool names for the has[tool] lookups
// the instruction builders gate their fragments on.
func toolSet(accessibleTools []string) map[string]bool {
	has := make(map[string]bool, len(accessibleTools))
	for _, t := range accessibleTools {
		has[t] = true
	}
	return has
}

// reuseBullet returns the "reuse what is known" instruction, naming `fetch` as the
// way to read a result in full only when the caller can reach it (fetch is
// registered alongside search but a persona may deny it); otherwise it names only
// the scoped drill-in tools, so the baseline never tells an agent to call a tool it
// cannot reach.
func reuseBullet(hasFetch bool) string {
	const head = "Reuse what is known. Treat `search` results as the starting point and "
	const tail = "rather than re-deriving an answer or re-asking the user for something already recorded."
	if hasFetch {
		return head + "read a result in full with `fetch` (pass the result's `reference`), or drill in " +
			"with the scoped tool a result points to, " + tail
	}
	return head + "drill in with the scoped tool a result points to, " + tail
}

// scriptsBullet returns the managed-script instruction: when a script is the
// right shape of work at all, and where the platform's own authoring guidance
// is. The pages are named by slug because a built-in page's row id is generated
// per deployment at reconcile time, so the slug is the only identifier stable
// enough to ship in this text (#1476). It names `fetch` as the way to read one
// only when the caller can reach it, exactly as the reuse bullet does.
func scriptsBullet(hasFetch bool) string {
	const head = "Settled, repeating work becomes a script. When the logic is worked out and the " +
		"work will repeat (a KPI report, a recurring export, a dashboard that refreshes on a " +
		"schedule), write it as a managed script with `manage_script` so it is re-run rather than " +
		"derived through a conversation again; keep using the query tools directly while you are " +
		"still exploring. Call `manage_script` with command `help` before writing your first one, " +
		"and read the platform's own authoring guidance rather than waiting for a search to " +
		"surface it: "
	const pages = "`mcp:knowledge_page:platform-writing-managed-scripts` for the dialect and the " +
		"authoring loop, `mcp:knowledge_page:platform-script-outputs-and-export-identity` for " +
		"where an output lands and what identity it keeps across runs, and " +
		"`mcp:knowledge_page:platform-semi-dynamic-dashboards` for choosing between composing a " +
		"whole document every run and refreshing only the data region of one a person can edit."
	if hasFetch {
		return head + "`fetch` " + pages
	}
	return head + "read the pages " + pages
}

// referencesBullet tells an agent to name a file from an asset's content
// rather than carry it, and points at the page that covers the mechanism.
//
// A tool schema can describe the `references` argument, and the portal
// toolkit's does; what it cannot do is reach an agent that is composing a
// document and has not thought to look for the argument at all. The decision
// is made while the markup is being written, which is before any tool call.
func referencesBullet(hasFetch bool) string {
	const head = "Name a file, do not carry it. When a document you save needs a logo, an image, a " +
		"design element, or a data table that is already in the platform, write its reference " +
		"where the file belongs in the markup and declare it in `references` on `save_asset` " +
		"instead of embedding the bytes: the file is stored once rather than once per version, " +
		"and replacing it refreshes every document naming it with no re-save. "
	const page = "`mcp:knowledge_page:platform-asset-references-and-the-refresh-loop` for the two " +
		"reference forms, the patterns an HTML or JSX document uses, and who can load the file " +
		"once it is declared."
	if hasFetch {
		return head + "`fetch` " + page
	}
	return head + "Read " + page
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
