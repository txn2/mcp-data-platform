// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PlatformInfo contains information about the platform deployment.
type PlatformInfo struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Toolkits    []string `json:"toolkits"`
	Features    Features `json:"features"`
}

// Features describes enabled platform features.
type Features struct {
	SemanticEnrichment bool `json:"semantic_enrichment"`
	QueryEnrichment    bool `json:"query_enrichment"`
	StorageEnrichment  bool `json:"storage_enrichment"`
	AuditLogging       bool `json:"audit_logging"`
}

// platformInfoInput is empty since this tool has no parameters.
type platformInfoInput struct{}

// registerInfoTool registers the platform_info tool with the MCP server.
func (p *Platform) registerInfoTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name: "platform_info",
		Description: "Get information about this MCP data platform deployment, including its purpose, " +
			"available toolkits, and enabled features. Call this first to understand what data and " +
			"capabilities are available.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ platformInfoInput) (*mcp.CallToolResult, any, error) {
		return p.handlePlatformInfo(ctx, req)
	})
}

// handlePlatformInfo handles the platform_info tool call.
func (p *Platform) handlePlatformInfo(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, any, error) {
	// Collect enabled toolkits
	var toolkits []string
	if p.config.Toolkits != nil {
		for kind := range p.config.Toolkits {
			toolkits = append(toolkits, kind)
		}
	}

	info := PlatformInfo{
		Name:        p.config.Server.Name,
		Version:     p.config.Server.Version,
		Description: p.config.Server.Description,
		Toolkits:    toolkits,
		Features: Features{
			SemanticEnrichment: p.config.Injection.TrinoSemanticEnrichment || p.config.Injection.S3SemanticEnrichment,
			QueryEnrichment:    p.config.Injection.DataHubQueryEnrichment,
			StorageEnrichment:  p.config.Injection.DataHubStorageEnrichment,
			AuditLogging:       p.config.Audit.Enabled,
		},
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Error: " + err.Error()},
			},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}
