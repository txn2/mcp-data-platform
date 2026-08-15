package scriptlayer

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolNameShowScripts is the MCP tool name of the presentation-only trigger
// that opens the portal's script pages for the human.
const ToolNameShowScripts = "show_scripts"

// showScriptsInput is the input for show_scripts. It carries no data
// operation; the optional search only pre-focuses the pages it opens.
type showScriptsInput struct {
	Search string `json:"search,omitempty"`
}

// registerShowScripts registers show_scripts: a presentation-only tool whose
// only job is to point the human at their scripts, their schedules, and what
// those have been producing.
//
// It follows the show_prompts split (#1040) for the same reason that one
// exists. A UI is for the human, but it is opened in response to a tool call,
// and tool calls are made by the agent; a presentation surface attached to the
// tool an agent uses for its own work puts a page in front of the user every
// time the agent reads a script. So the two are separate tools: every script
// operation an agent performs — create, edit, validate, dry-run, read a run —
// lives on manage_script and renders nothing, and this tool does no data work
// an agent would ever call it for.
//
// It is registered wherever scripts are kept, including a deployment that
// cannot execute them: what a script is and what it last did are worth seeing
// even where nothing will run one. RegisterTool has already established that
// there is a store, so this carries no guard of its own.
func (h *Handle) registerShowScripts(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  ToolNameShowScripts,
		Title: "Show Scripts",
		Description: "Open the user's managed scripts in the portal: what they own, what each one is " +
			"scheduled to do, and how its recent runs went. This is the correct tool whenever the human " +
			"asks to see, list, view, show, or browse their scripts, schedules, or automations, or asks " +
			"what is running, what ran, or why something did not update: \"show me my scripts\", \"what " +
			"automations do I have\", \"did the daily report run\". It opens pages for the human and needs " +
			"no follow-up tool calls. Use manage_script instead to create, edit, validate, or dry-run a " +
			"script, or when you need script or run data for your own reasoning rather than to show it.",
		InputSchema: showScriptsSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input showScriptsInput) (*mcp.CallToolResult, any, error) {
		return h.handleShowScripts(ctx, input)
	})
}

// handleShowScripts returns the address of the pages and a short confirmation.
// It carries no script data, which is what keeps the tool useless to an agent
// as a data source: an agent that wants to know something about a script has
// manage_script, and one that wants to show the human their automations has
// this.
//
// The URL is absent when the deployment has not been told its own public
// address, in which case the confirmation still names where to look. Guessing
// one from a request would produce a link that works from inside the cluster
// and nowhere else.
func (h *Handle) handleShowScripts(_ context.Context, input showScriptsInput) (*mcp.CallToolResult, any, error) {
	result := map[string]any{
		"shown":   true,
		"message": "Opened the script pages in the portal, where the user can see each script, its schedule, and its run history.",
		"hint": "This shows a page to the human. To read a script, its runs, or its log yourself, " +
			"use manage_script.",
	}
	if h.portalURL != "" {
		result["url"] = h.portalURL + "/portal/scripts"
	}
	if input.Search != "" {
		result["search"] = input.Search
	}
	return jsonResult(result)
}

// showScriptsSchema returns the JSON schema for show_scripts.
func showScriptsSchema() any {
	return map[string]any{
		keyType:                valObject,
		"additionalProperties": false,
		"properties": map[string]any{
			"search": map[string]any{
				keyType:        valString,
				keyDescription: "Optional term to pre-focus the pages on matching scripts.",
			},
		},
	}
}
