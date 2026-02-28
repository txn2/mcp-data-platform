// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// autoPromptName is the name of the automatically-registered platform overview prompt.
const autoPromptName = "platform-overview"

// registerPlatformPrompts registers platform-level prompts from config.
// It first registers the auto-generated platform overview prompt (if applicable),
// then registers operator-configured prompts.
func (p *Platform) registerPlatformPrompts() {
	p.registerAutoPrompt()
	for _, promptCfg := range p.config.Server.Prompts {
		p.registerPrompt(promptCfg)
	}
}

// registerAutoPrompt registers the auto-generated "platform-overview" prompt when
// server.description is non-empty. It is skipped if an operator-configured prompt
// already uses the name "platform-overview".
func (p *Platform) registerAutoPrompt() {
	if p.config.Server.Description == "" {
		return
	}

	// Skip if operator has already defined a prompt with this name.
	for _, promptCfg := range p.config.Server.Prompts {
		if promptCfg.Name == autoPromptName {
			return
		}
	}

	content := fmt.Sprintf("%s\n\nTo get full capabilities, connected toolkits, and agent instructions, call the\n`platform_info` tool.", p.config.Server.Description)

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        autoPromptName,
		Title:       p.config.Server.Name,
		Description: "Overview of this data platform — what it covers and how to use it",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return buildPromptResult(content), nil
	})
}

// registerPrompt registers a single prompt with the MCP server.
func (p *Platform) registerPrompt(cfg PromptConfig) {
	// Capture cfg in closure to avoid loop variable issues
	promptContent := cfg.Content

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        cfg.Name,
		Description: cfg.Description,
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return buildPromptResult(promptContent), nil
	})
}

// buildPromptResult creates a GetPromptResult with the given content.
func buildPromptResult(content string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Messages: []*mcp.PromptMessage{
			{
				Role: "user",
				Content: &mcp.TextContent{
					Text: content,
				},
			},
		},
	}
}
