package promptlayer

import (
	"context"

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
		Description: "Displays the user's prompt library as an interactive browser for the human to look at. " +
			"Call this ONLY when the human wants to see, browse, or pick from their prompts visually " +
			"(\"show me my prompts\", \"open my prompt library\"). It renders a UI panel and does no data " +
			"work, so never call it for your own reasoning or as a way to read prompt data. For running, " +
			"creating, editing, or listing prompts as part of your work, use manage_prompt, which returns " +
			"data and renders no UI.",
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
		schemaKeyType: "object",
		"properties": map[string]any{
			"search": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Optional term to pre-focus the library on matching prompts.",
			},
		},
	}
}
