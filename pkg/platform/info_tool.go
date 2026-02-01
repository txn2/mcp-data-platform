// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PlatformInfo contains information about the platform deployment.
type PlatformInfo struct {
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	AgentInstructions string   `json:"agent_instructions,omitempty"`
	Toolkits          []string `json:"toolkits"`
	Features          Features `json:"features"`
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
		Name:        "platform_info",
		Description: p.buildInfoToolDescription(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ platformInfoInput) (*mcp.CallToolResult, any, error) {
		return p.handlePlatformInfo(ctx, req)
	})
}

// buildInfoToolDescription builds a dynamic tool description based on configuration.
func (p *Platform) buildInfoToolDescription() string {
	base := "Get information about this MCP data platform"
	if p.config.Server.Name != "" && p.config.Server.Name != "mcp-data-platform" {
		base = fmt.Sprintf("Get information about %s", p.config.Server.Name)
	}
	if len(p.config.Server.Tags) > 0 {
		base += fmt.Sprintf(" (%s)", strings.Join(p.config.Server.Tags, ", "))
	}
	return base + ", including its purpose, available toolkits, and enabled features. " +
		"Call this first to understand what data and capabilities are available."
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
		Name:              p.config.Server.Name,
		Version:           p.config.Server.Version,
		Description:       p.config.Server.Description,
		Tags:              p.config.Server.Tags,
		AgentInstructions: p.config.Server.AgentInstructions,
		Toolkits:          toolkits,
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
