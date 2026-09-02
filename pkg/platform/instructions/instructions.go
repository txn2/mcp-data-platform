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
// deployment. It is composed beneath the admin-configured business context
// (server.agent_instructions) rather than re-authored per deployment, and it is
// versioned with the binary so upgrading the platform updates it everywhere with
// no per-deployment edits.
//
// It is an index, not a manual (#1586). A tool's own schema and description
// document how to call it; what a schema cannot do is tell an agent that a
// capability exists at all, or that the platform ships written guidance on it.
// So each line names one capability, its entry-point tool, and the judgment the
// agent has to make before reaching for it -- and the depth lives in the
// built-in knowledge pages the second section indexes, which are fetched when
// they are needed rather than carried in every session's first response.
//
// It names a tool only when that tool is in accessibleTools, so the baseline
// never tells an agent to call a tool its persona cannot reach or that the
// deployment did not enable. accessibleTools is the set of tool names available
// to the caller: registered on the platform and, for a per-caller baseline,
// allowed by the caller's persona. A set with none of the baseline's tools
// yields an empty baseline, since there is nothing to say without a tool to name.
func Build(accessibleTools []string) string {
	has := toolSet(accessibleTools)

	bullets := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		if has[c.tool] {
			bullets = append(bullets, c.line(has))
		}
	}
	if len(bullets) == 0 {
		return ""
	}

	lines := make([]string, 0, len(bullets)+3)
	lines = append(lines, "How to operate this platform:")
	for _, bullet := range bullets {
		lines = append(lines, "- "+bullet)
	}
	if pages := pageIndex(has); pages != "" {
		lines = append(lines, "", pages)
	}
	return strings.Join(lines, "\n")
}

// capability is one line of the index: the tool that must be reachable for the
// line to appear, and the line itself. Holding them together is what keeps a
// line from naming a tool its own gate does not check.
type capability struct {
	tool string
	line func(has map[string]bool) string
}

// capabilities is the index, in the order an agent meets them: discover, reuse
// what discovery found, resolve a named procedure, settle repeating work into a
// script, record what was learned, promote it, and the two judgments that are
// made while composing rather than at a tool call.
var capabilities = []capability{
	{tool: toolSearch, line: func(map[string]bool) string {
		return "Discover before you act. `search` is the one way in, and one query covers every " +
			"source you can reach: the data catalog and its governance, context documents, " +
			"knowledge pages, your memory, captured insights, feedback, saved assets, uploaded " +
			"reference material, prompts, managed scripts, recorded calls and sessions, API " +
			"endpoints, and connections. The answer may span several of them, or may not be in " +
			"the data warehouse at all, so do not assume a backend and do not stop at the first " +
			"result."
	}},
	{tool: toolSearch, line: func(has map[string]bool) string { return reuseBullet(has[toolFetch]) }},
	{tool: toolManagePrompt, line: func(map[string]bool) string {
		return "Named procedures are prompts. Resolve what the user names -- a report, a " +
			"procedure, a recurring task -- with `manage_prompt` command `use`, which takes the " +
			"name as given (stored name, display name, `mcp:prompt:<id>`, or free text). Resolve " +
			"rather than enumerate."
	}},
	{tool: toolManageScript, line: func(map[string]bool) string {
		return "Settled, repeating work becomes a script. Keep using the query tools while you " +
			"are still exploring; once the logic is worked out and the work will repeat, write " +
			"it with `manage_script` (command `help` first) so it is re-run rather than " +
			"re-derived through a conversation."
	}},
	{tool: toolMemoryCapture, line: func(map[string]bool) string {
		return "Capture what you learn, with the call that proves it. `memory_capture` records a " +
			"durable finding. Every query and API call returns its own `call_id`: when the " +
			"result met the purpose you stated and the statement is worth running again, name " +
			"it as an `mcp:call:<id>` in `memory_capture` `sources`, including when the answer " +
			"only went into the conversation."
	}},
	{tool: toolApplyKnowledge, line: func(map[string]bool) string {
		return "Promote what holds. `apply_knowledge` drives the review queue captures enter: " +
			"action `bulk_review` with `itemize:true` lists it, a dataset-specific fact promotes " +
			"to a DataHub entity (`urn:li:...`) and broader domain knowledge to a knowledge page " +
			"(`mcp:<type>:<key>`). The two namespaces never mix."
	}},
	{tool: toolSaveAsset, line: func(map[string]bool) string {
		return "Name a file, do not carry it. When a document you save needs a logo, an image, or " +
			"a data table already in the platform, write its reference where the file belongs in " +
			"the markup and declare it in `references` on `save_asset` instead of embedding the " +
			"bytes."
	}},
	{tool: toolTrinoQuery, line: func(map[string]bool) string {
		return "A short list of outside keys needs no table. Join a pasted list of ids inline " +
			"through `trino_query` -- `JOIN (VALUES ('a'),('b')) AS t(id)` or " +
			"`WHERE id IN (...)` -- rather than asking for a table to be created; above a few " +
			"thousand rows, upload the file, register it as a table, and join the registered " +
			"table by name."
	}},
}

// Built-in knowledge page slugs the baseline indexes. A built-in page's row id
// is generated per deployment at reconcile time, so the slug is the only handle
// the shipped text can name (#1476). They are constants here so
// TestBaselinePagesAreShipped can bind every one to a page the binary actually
// carries; a slug renamed on one side and not the other would otherwise leave
// the baseline pointing an agent at nothing.
const (
	PageWritingManagedScripts = "platform-writing-managed-scripts"
	PageScriptOutputs         = "platform-script-outputs-and-export-identity"
	PageSemiDynamicDashboards = "platform-semi-dynamic-dashboards"
	PageAssetReferences       = "platform-asset-references-and-the-refresh-loop"
	PageProvenanceCapture     = "platform-provenance-and-the-capture-loop"
	PageContentTypes          = "platform-content-types-for-stored-files"
)

// knowledgePageRef is the reference form `fetch` resolves a built-in page by.
func knowledgePageRef(slug string) string { return "`mcp:knowledge_page:" + slug + "`" }

// baselinePage is one entry of the page index: the tool whose capability the
// page documents, the page's slug, and what reading it answers.
type baselinePage struct {
	tool  string
	slug  string
	about string
}

// baselinePages is the shipped guidance an agent should know exists. Each is
// gated on the capability it documents, so a persona that cannot write scripts
// is not handed three pages about writing them.
var baselinePages = []baselinePage{
	{
		toolManageScript, PageWritingManagedScripts,
		"the Starlark dialect and its deliberate absences, what a script may call and the persona that decides it, and the validate/dry-run loop a save follows",
	},
	{
		toolManageScript, PageScriptOutputs,
		"where a script's output lands and what identity it keeps across runs: a stable name refreshes one asset, a dated name archives",
	},
	{
		toolManageScript, PageSemiDynamicDashboards,
		"composing a whole document every run versus publishing one whose data region a scheduled script refreshes",
	},
	{
		toolSaveAsset, PageAssetReferences,
		"the two reference forms, the patterns an HTML or JSX document uses, and who can load a declared file",
	},
	{
		toolMemoryCapture, PageProvenanceCapture,
		"naming sources so an asset's provenance is exact, and the loop that turns session knowledge into reviewed catalog knowledge",
	},
	{
		toolSaveAsset, PageContentTypes,
		"the media type every stored file carries, the families detection cannot name from bytes, and what a write must declare",
	},
}

// pageIndex renders the shipped-guidance index for the pages this caller's
// capabilities make relevant, or "" when none are. It exists because a search
// only surfaces a page once an agent already suspects it needs one; naming the
// pages up front is what makes the platform's own guidance reachable on the
// first attempt rather than the third.
//
// Without `fetch` a slug cannot be dereferenced, so the index says to find the
// pages through `search` instead of handing over references the caller cannot
// follow.
func pageIndex(has map[string]bool) string {
	entries := make([]string, 0, len(baselinePages))
	for _, p := range baselinePages {
		if !has[p.tool] {
			continue
		}
		entries = append(entries, "- "+knowledgePageRef(p.slug)+" -- "+p.about+".")
	}
	if len(entries) == 0 {
		return ""
	}

	// The index is only worth carrying for a caller who can read a page. Naming
	// a tool the caller cannot reach would break the baseline's own rule, so a
	// caller with neither way in is given no index rather than references it
	// cannot follow.
	const head = "What the platform documents about itself. These pages ship with the platform; "
	var reach string
	switch {
	case has[toolFetch]:
		reach = "`fetch` one by the reference named here rather than waiting for a search to surface it:"
	case has[toolSearch]:
		reach = "`search` finds one by the slug named here:"
	default:
		return ""
	}
	return strings.Join(append([]string{head + reach}, entries...), "\n")
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
		"trino_describe_table, s3_list, etc.). Then call search, the one way to " +
		"discover, to reuse what is already known before re-asking the user or re-deriving it. " +
		"Skipping these causes incorrect query routing, operational rule violations, and degraded output quality."
}
