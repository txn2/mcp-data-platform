package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HintsResource provides tool hints as an MCP resource.
const hintsResourceURI = "hints://operational"

// registerHintsResource registers the hints resource with the MCP server.
func (p *Platform) registerHintsResource() {
	p.mcpServer.AddResource(&mcp.Resource{
		URI:         hintsResourceURI,
		Name:        "Tool Hints",
		Description: "Operational hints and guidance for using platform tools effectively",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return p.buildHintsResourceResult()
	})
}

// buildHintsResourceResult creates the resource result with all hints.
func (p *Platform) buildHintsResourceResult() (*mcp.ReadResourceResult, error) {
	hints := p.hintManager.All()

	content, err := json.MarshalIndent(hints, "", "  ")
	if err != nil {
		return nil, err
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      hintsResourceURI,
				MIMEType: "application/json",
				Text:     string(content),
			},
		},
	}, nil
}
