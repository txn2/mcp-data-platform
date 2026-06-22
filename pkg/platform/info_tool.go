// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	personapkg "github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// resourcesDiscoverabilityNote is the runtime note appended to agent
// instructions when managed resources are enabled, pointing the agent at the
// MCP resources primitive for uploaded reference material.
const resourcesDiscoverabilityNote = "Uploaded resources (samples, playbooks, templates, references) " +
	"are available via the MCP resources primitive. Call resources/list to discover " +
	"what reference material has been uploaded. Use these resources when the task " +
	"involves user-provided context, examples, formatting specifications, or reference data."

// Info contains information about the platform deployment.
type Info struct {
	Name                string                `json:"name"`
	Version             string                `json:"version"`
	Description         string                `json:"description,omitempty"`
	Tags                []string              `json:"tags,omitempty"`
	AgentInstructions   string                `json:"agent_instructions,omitempty"`
	Toolkits            []string              `json:"toolkits"`
	ToolkitDescriptions map[string]string     `json:"toolkit_descriptions,omitempty"`
	PortalURL           string                `json:"portal_url,omitempty"`
	Persona             *PersonaInfo          `json:"persona,omitempty"`
	Prompts             []registry.PromptInfo `json:"prompts,omitempty"`
	Features            Features              `json:"features"`
	ConfigVersion       ConfigVersionInfo     `json:"config_version"`
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
	ManagedResources   bool                `json:"managed_resources"`
}

// KnowledgeApplyInfo provides information about the knowledge apply feature.
type KnowledgeApplyInfo struct {
	Enabled           bool   `json:"enabled"`
	DataHubConnection string `json:"datahub_connection,omitempty"`
}

// buildFeatures constructs the Features struct from platform config.
func (p *Platform) buildFeatures() Features {
	// Enrichment is reported on only when both its flag is enabled (default-on)
	// AND the provider that performs it is configured. Reporting it on without a
	// provider would mislead an agent into expecting context the platform cannot
	// produce. Trino/S3 enrichment draws on the semantic provider; DataHub query
	// enrichment on the query provider; DataHub storage enrichment on storage.
	f := Features{
		SemanticEnrichment: p.semanticProvider != nil &&
			(p.config.Injection.IsTrinoSemanticEnrichmentEnabled() || p.config.Injection.IsS3SemanticEnrichmentEnabled()),
		QueryEnrichment:   p.queryProvider != nil && p.config.Injection.IsDataHubQueryEnrichmentEnabled(),
		StorageEnrichment: p.storageProvider != nil && p.config.Injection.IsDataHubStorageEnrichmentEnabled(),
		AuditLogging:      !isExplicitlyDisabled(p.config.Audit.Enabled),
		KnowledgeCapture:  !isExplicitlyDisabled(p.config.Knowledge.Enabled),
		ManagedResources:  p.resourceStore != nil,
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

// platformInfoTitle is the fallback display name when server.name is the
// default mcp-data-platform identifier.
const platformInfoTitle = "Platform Info"

// registerInfoTool registers the platform_info tool with the MCP server.
func (p *Platform) registerInfoTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        defaultInitTool,
		Title:       instructions.InfoToolTitle(p.config.Server.Name, defaultServerName, platformInfoTitle),
		Description: instructions.InfoToolDescription(p.config.Server.Name, defaultServerName, p.config.Server.Tags),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ platformInfoInput) (*mcp.CallToolResult, any, error) {
		return p.handleInfo(ctx, req)
	})
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

	// Prepend "platform" — always-present toolkit for platform_info, list_connections, etc.
	toolkits = append([]string{kindPlatform}, toolkits...)
	if toolkitDescriptions == nil {
		toolkitDescriptions = make(map[string]string)
	}
	if toolkitDescriptions[kindPlatform] == "" {
		toolkitDescriptions[kindPlatform] = "Core platform tools: deployment info, connection listing, and resource access."
	}

	// Resolve the caller's persona: prefer the one set by auth middleware,
	// fall back to the configured default.
	persona := p.resolveCallerPersona(ctx)

	// Apply the persona description override; the persona's agent-instruction
	// tuning is applied to the admin layer inside ComposeForCaller below.
	description := p.config.Server.Description
	var caller *personapkg.Persona
	if persona != nil {
		if full, ok := p.personaRegistry.Get(persona.Name); ok {
			caller = full
			description = full.ApplyDescription(description)
		}
	}

	// Compose the full instruction stack: the platform baseline (gated to the
	// tools this caller may reach) beneath the admin business context, with the
	// resources nudge appended as a runtime note when managed resources exist.
	var notes []string
	if p.resourceStore != nil {
		notes = append(notes, resourcesDiscoverabilityNote)
	}
	agentInstructions := instructions.ComposeForCaller(
		p.config.Server.AgentInstructions,
		p.toolkitRegistry.AllTools(),
		caller,
		p.personaRegistry,
		notes...,
	)

	reg := DefaultRegistry()
	info := Info{
		Name:                p.config.Server.Name,
		Version:             p.config.Server.Version,
		Description:         description,
		Tags:                p.config.Server.Tags,
		AgentInstructions:   agentInstructions,
		Toolkits:            toolkits,
		ToolkitDescriptions: toolkitDescriptions,
		PortalURL:           p.config.Portal.PublicBaseURL,
		Persona:             persona,
		Prompts:             p.AllPromptInfos(),
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
