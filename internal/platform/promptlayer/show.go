package promptlayer

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/promptschema"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolNameShowPrompts is the MCP tool name of the presentation-only prompt
// library trigger, exported for the composition root that binds the
// prompt-browser MCP App to it (#1040).
const ToolNameShowPrompts = "show_prompts"

// showPromptsInput is the input for show_prompts. It carries no data operation;
// the optional search only pre-focuses the rendered library.
type showPromptsInput struct {
	Search string `json:"search,omitempty"`
}

// RegisterShowPromptsTool registers show_prompts: a presentation-only tool whose
// only job is to render the prompt-library browser for the human in MCP
// Apps-capable hosts.
//
// An MCP App is a UI for the human, but the protocol renders it in response to a
// tool call, and tool calls are made by the agent. Binding the app to a tool
// that also does data work (manage_prompt) means every agent prompt operation
// renders the UI, uninvited. This tool separates the two: it performs no data
// work an agent would ever call it for, so an agent has no reason to invoke it
// except when the human asks to see their prompts. Every prompt operation
// (resolve, run, create, edit, and listing for the agent's own reasoning) lives
// on manage_prompt, which carries no app and renders nothing. The rendered app
// hydrates itself with its own manage_prompt calls, so this tool returns only a
// short confirmation.
//
// No-op on a nil Handle or a no-DB deployment (no prompts to show).
func (h *Handle) RegisterShowPromptsTool(server *mcp.Server) {
	if h == nil || h.store == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:  ToolNameShowPrompts,
		Title: "Show Prompt Library",
		Description: "Open the user's prompt library as an interactive visual browser (search, filter, " +
			"preview, and one-click run). This is the correct tool whenever the human asks to see, list, " +
			"view, show, browse, or pick their prompts, or asks what prompts they have or what is in their " +
			"library: \"list my prompts\", \"show me my prompts\", \"view my prompts\", \"what prompts do I " +
			"have\", \"open my prompt library\", \"browse my prompts\". Prefer it over manage_prompt for any " +
			"human-facing request to view prompts; it renders the library for the human and needs no " +
			"follow-up tool calls. Use manage_prompt only to run, create, or edit a prompt, or when you need " +
			"prompt data for your own reasoning rather than to show it to the human.",
		InputSchema: showPromptsSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input showPromptsInput) (*mcp.CallToolResult, any, error) {
		return h.handleShowPrompts(ctx, input)
	})
}

// handleShowPrompts returns a short confirmation. The rendered app populates
// itself from its own manage_prompt calls, so this result carries no prompt
// data (which is also what keeps the tool useless to an agent as a data source).
// On a host that does not render apps, the message steers the caller to the data
// tool instead.
func (*Handle) handleShowPrompts(_ context.Context, input showPromptsInput) (*mcp.CallToolResult, any, error) {
	result := map[string]any{
		"shown":   true,
		"message": "Opened the prompt library in the panel for the user to browse, search, and run.",
		"hint": "This renders a UI for the human. For a text list, or to run or edit a prompt yourself, " +
			"use manage_prompt.",
	}
	if input.Search != "" {
		result["search"] = input.Search
	}
	return promptJSONResult(result)
}

// showPromptsSchema returns the JSON schema for show_prompts.
func showPromptsSchema() any {
	return map[string]any{
		promptschema.KeyType:   "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"search": map[string]any{
				promptschema.KeyType:        promptschema.ValString,
				promptschema.KeyDescription: "Optional term to pre-focus the library on matching prompts.",
			},
		},
	}
}
