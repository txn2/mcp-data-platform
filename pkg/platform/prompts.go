// Package platform provides the main platform orchestration.
package platform

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPlatformPrompts registers platform-level prompts from config.
func (p *Platform) registerPlatformPrompts() {
	for _, promptCfg := range p.config.Server.Prompts {
		p.registerPrompt(promptCfg)
	}
}

// registerPrompt registers a single prompt with the MCP server.
func (p *Platform) registerPrompt(cfg PromptConfig) {
	// Capture cfg in closure to avoid loop variable issues
	promptContent := cfg.Content

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        cfg.Name,
		Description: cfg.Description,
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role: "user",
					Content: &mcp.TextContent{
						Text: promptContent,
					},
				},
			},
		}, nil
	})
}
