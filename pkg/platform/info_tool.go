// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Info contains information about the platform deployment.
type Info struct {
	Name                string            `json:"name"`
	Version             string            `json:"version"`
	Description         string            `json:"description,omitempty"`
	Tags                []string          `json:"tags,omitempty"`
	AgentInstructions   string            `json:"agent_instructions,omitempty"`
	Toolkits            []string          `json:"toolkits"`
	ToolkitDescriptions map[string]string `json:"toolkit_descriptions,omitempty"`
	Persona             *PersonaInfo      `json:"persona,omitempty"`
	Features            Features          `json:"features"`
	ConfigVersion       ConfigVersionInfo `json:"config_version"`
}

// ConfigVersionInfo provides information about the config API version.
type ConfigVersionInfo struct {
	APIVersion        string   `json:"api_version"`
	SupportedVersions []string `json:"supported_versions"`
	LatestVersion     string   `json:"latest_version"`
}

// PersonaInfo provides summary information about a persona.
type PersonaInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

// Features describes enabled platform features.
type Features struct {
	SemanticEnrichment bool                `json:"semantic_enrichment"`
	QueryEnrichment    bool                `json:"query_enrichment"`
	StorageEnrichment  bool                `json:"storage_enrichment"`
	AuditLogging       bool                `json:"audit_logging"`
	KnowledgeCapture   bool                `json:"knowledge_capture"`
	KnowledgeApply     *KnowledgeApplyInfo `json:"knowledge_apply,omitempty"`
}

// KnowledgeApplyInfo provides information about the knowledge apply feature.
type KnowledgeApplyInfo struct {
	Enabled           bool   `json:"enabled"`
	DataHubConnection string `json:"datahub_connection,omitempty"`
}

// buildFeatures constructs the Features struct from platform config.
func (p *Platform) buildFeatures() Features {
	f := Features{
		SemanticEnrichment: p.config.Injection.TrinoSemanticEnrichment || p.config.Injection.S3SemanticEnrichment,
		QueryEnrichment:    p.config.Injection.DataHubQueryEnrichment,
		StorageEnrichment:  p.config.Injection.DataHubStorageEnrichment,
		AuditLogging:       p.config.Audit.Enabled,
		KnowledgeCapture:   p.config.Knowledge.Enabled,
	}

	if p.config.Knowledge.Apply.Enabled {
		f.KnowledgeApply = &KnowledgeApplyInfo{
			Enabled:           true,
			DataHubConnection: p.config.Knowledge.Apply.DataHubConnection,
		}
	}

	return f
}

// resolveCallerPersona returns a PersonaInfo for the calling user.
// It reads the persona name from PlatformContext (set by auth middleware) and
// looks it up in the registry. If no persona is found in context, it falls back
// to the configured default persona. Returns nil when no persona applies.
func (p *Platform) resolveCallerPersona(ctx context.Context) *PersonaInfo {
	name := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		name = pc.PersonaName
	}
	if name == "" {
		if def, ok := p.personaRegistry.GetDefault(); ok {
			name = def.Name
		}
	}
	if name == "" {
		return nil
	}
	pers, ok := p.personaRegistry.Get(name)
	if !ok {
		return nil
	}
	return &PersonaInfo{
		Name:        pers.Name,
		DisplayName: pers.DisplayName,
		Description: pers.Description,
	}
}

// platformInfoInput is empty since this tool has no parameters.
type platformInfoInput struct{}

// registerInfoTool registers the platform_info tool with the MCP server.
func (p *Platform) registerInfoTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        "platform_info",
		Title:       p.buildInfoToolTitle(),
		Description: p.buildInfoToolDescription(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ platformInfoInput) (*mcp.CallToolResult, any, error) {
		return p.handleInfo(ctx, req)
	})
}

// buildInfoToolTitle returns a human-readable display name for the platform_info tool.
// When server.name is set to a custom value, it is used as the title so that
// Claude Desktop shows e.g. "ACME Data Platform" instead of "platform_info".
func (p *Platform) buildInfoToolTitle() string {
	if p.config.Server.Name != "" && p.config.Server.Name != "mcp-data-platform" {
		return p.config.Server.Name
	}
	return "Platform Info"
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

// collectToolkits returns the list of enabled toolkit names and any
// operator-provided descriptions extracted from the toolkit config map.
func (p *Platform) collectToolkits() (names []string, descriptions map[string]string) {
	for kind, cfg := range p.config.Toolkits {
		names = append(names, kind)
		m, ok := cfg.(map[string]any)
		if !ok {
			continue
		}
		desc, ok := m["description"].(string)
		if !ok || desc == "" {
			continue
		}
		if descriptions == nil {
			descriptions = make(map[string]string)
		}
		descriptions[kind] = desc
	}
	return names, descriptions
}

// handleInfo handles the platform_info tool call.
func (p *Platform) handleInfo(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, any, error) {
	toolkits, toolkitDescriptions := p.collectToolkits()

	// Resolve the caller's persona: prefer the one set by auth middleware,
	// fall back to the configured default.
	persona := p.resolveCallerPersona(ctx)

	reg := DefaultRegistry()
	info := Info{
		Name:                p.config.Server.Name,
		Version:             p.config.Server.Version,
		Description:         p.config.Server.Description,
		Tags:                p.config.Server.Tags,
		AgentInstructions:   p.config.Server.AgentInstructions,
		Toolkits:            toolkits,
		ToolkitDescriptions: toolkitDescriptions,
		Persona:             persona,
		Features:            p.buildFeatures(),
		ConfigVersion: ConfigVersionInfo{
			APIVersion:        p.config.APIVersion,
			SupportedVersions: reg.ListSupported(),
			LatestVersion:     reg.Current(),
		},
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return &mcp.CallToolResult{ //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError, not as Go errors
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
