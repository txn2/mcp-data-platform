// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/notices"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	personapkg "github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

// Info contains information about the platform deployment.
type Info struct {
	Name                string            `json:"name"`
	Version             string            `json:"version"`
	Description         string            `json:"description,omitempty"`
	Tags                []string          `json:"tags,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	SessionExpiresAt    string            `json:"session_expires_at,omitempty"`
	AgentInstructions   string            `json:"agent_instructions,omitempty"`
	Toolkits            []string          `json:"toolkits"`
	ToolkitDescriptions map[string]string `json:"toolkit_descriptions,omitempty"`
	PortalURL           string            `json:"portal_url,omitempty"`
	Persona             *PersonaInfo      `json:"persona,omitempty"`
	// Notices is what is waiting for this caller: unresolved feedback other
	// people left on assets they own, and artifacts newly shared with them
	// (#1278). Absent when there is nothing to report. Delivering it advances
	// the caller's watermark, so it is shown once.
	Notices       *Notices          `json:"notices,omitempty"`
	Features      Features          `json:"features"`
	ConfigVersion ConfigVersionInfo `json:"config_version"`
}

// Notices is the caller's session-start digest, aliased so a library consumer
// can name the type the Info field carries.
type Notices = notices.Digest

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
	Enabled           bool             `json:"enabled"`
	DataHubConnection string           `json:"datahub_connection,omitempty"`
	ReviewQueue       *ReviewQueueInfo `json:"review_queue,omitempty"`
}

// ReviewQueueInfo is the pending-review summary platform_info reports, aliased
// so a library consumer can name the type KnowledgeApplyInfo carries.
type ReviewQueueInfo = knowledgelayer.ReviewQueueInfo

// Tool names whose platform_info feature flags are gated by persona access, so a
// persona that cannot reach the tool is not told the capability exists (#686).
const (
	toolMemoryCapture  = "memory_capture"
	toolApplyKnowledge = "apply_knowledge"
)

// buildFeatures constructs the Features struct from platform config, gating the
// knowledge feature flags to the tools the caller's persona can actually reach
// (accessibleTools) so the orientation never advertises a lifecycle the caller
// cannot drive.
func (p *Platform) buildFeatures(ctx context.Context, accessibleTools []string) Features {
	canReach := func(tool string) bool { return slices.Contains(accessibleTools, tool) }
	// Enrichment is reported on only when both its flag is enabled (default-on)
	// AND the provider that performs it is configured. Reporting it on without a
	// provider would mislead an agent into expecting context the platform cannot
	// produce. Trino/S3 enrichment draws on the semantic provider; DataHub query
	// enrichment on the query provider; DataHub storage enrichment on storage.
	f := Features{
		SemanticEnrichment: p.semanticProvider != nil &&
			(p.config.Enrichment.IsTrinoSemanticEnrichmentEnabled() || p.config.Enrichment.IsS3SemanticEnrichmentEnabled()),
		QueryEnrichment:   p.queryProvider != nil && p.config.Enrichment.IsDataHubQueryEnrichmentEnabled(),
		StorageEnrichment: p.storageProvider != nil && p.config.Enrichment.IsDataHubStorageEnrichmentEnabled(),
		AuditLogging:      !isExplicitlyDisabled(p.config.Audit.Enabled),
		KnowledgeCapture:  canReach(toolMemoryCapture) && !isExplicitlyDisabled(p.config.Knowledge.Enabled),
		ManagedResources:  p.resources.Store() != nil,
	}

	if p.config.Knowledge.Apply.IsEnabled() && canReach(toolApplyKnowledge) {
		f.KnowledgeApply = &KnowledgeApplyInfo{
			Enabled:           true,
			DataHubConnection: p.config.Knowledge.Apply.DataHubConnection,
			ReviewQueue:       p.knowledge.PendingReviewSummary(ctx),
		}
	}

	return f
}

// resolveCallerPersona returns a PersonaInfo for the calling user.
// It reads the persona name from PlatformContext (set by auth middleware) and
// looks it up in the registry. Returns nil when the caller's roles mapped to no
// persona, which platform_info reports as no persona rather than substituting
// one the caller was never granted.
func (p *Platform) resolveCallerPersona(ctx context.Context) *PersonaInfo {
	name := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		name = pc.PersonaName
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
		Name:         defaultInitTool,
		Title:        instructions.InfoToolTitle(p.config.Server.Name, defaultServerName, platformInfoTitle),
		Description:  instructions.InfoToolDescription(p.config.Server.Name, defaultServerName, p.config.Server.Tags),
		OutputSchema: infoOutputSchema,
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
	description := p.config.ServerDescription(ctx)
	var caller *personapkg.Persona
	if persona != nil {
		if full, ok := p.personaRegistry.Get(persona.Name); ok {
			caller = full
			description = full.ApplyDescription(description)
		}
	}

	// The tools this caller's persona may reach gate the instruction baseline, the
	// resources note, and the knowledge feature flags, so a persona is never told
	// about a capability it cannot drive.
	// Every registered tool, the platform's own included: gating on the toolkit
	// registry alone withholds the baseline's prompt and script guidance from
	// every caller, because those tools are registered outside it (#1586).
	registeredTools := RegisteredToolNames(p.toolkitRegistry.AllTools(), p.PlatformTools())
	accessibleTools := instructions.AccessibleTools(registeredTools, caller, p.personaRegistry)

	// Compose the full instruction stack: the platform baseline (gated to the
	// tools this caller may reach) beneath the admin business context, with the
	// resources positioning and operating rule appended as a runtime note when
	// managed resources exist.
	var notes []string
	if p.resources.Store() != nil {
		notes = append(notes, instructions.ResourcesNote(accessibleTools))
	}
	if p.config.Purpose.IsEnabled() {
		notes = append(notes, instructions.PurposeNote(p.config.Purpose.IsRequired()))
	}
	digest := p.portalStore.Notices().Build(ctx, middleware.GetPlatformContext(ctx))
	feedbackCount, shareCount := digest.Counts()
	notes = append(notes, instructions.NoticesNote(accessibleTools, feedbackCount, shareCount))
	agentInstructions := instructions.ComposeForCaller(
		p.config.ServerAgentInstructions(ctx),
		registeredTools,
		caller,
		p.personaRegistry,
		notes...,
	)

	// Mint an explicit session handle (#792). platform_info is the only tool
	// that mints; every other tool requires the returned session_id, which
	// makes platform_info structurally unskippable and gives every downstream
	// consumer (gates, provenance, audit) a deliberate session key.
	sessionID, sessionExpiresAt := p.mintSessionHandle(ctx, personaName(persona))
	if sessionID != "" {
		agentInstructions = sessionThreadingInstruction(sessionID) + agentInstructions
	}

	reg := defaultRegistry()
	info := Info{
		Name:                p.config.Server.Name,
		Version:             p.config.Server.Version,
		Description:         description,
		Tags:                p.config.Server.Tags,
		SessionID:           sessionID,
		SessionExpiresAt:    sessionExpiresAt,
		AgentInstructions:   agentInstructions,
		Toolkits:            toolkits,
		ToolkitDescriptions: toolkitDescriptions,
		PortalURL:           p.config.Portal.PublicBaseURL,
		Persona:             persona,
		Notices:             digest,
		Features:            p.buildFeatures(ctx, accessibleTools),
		ConfigVersion: ConfigVersionInfo{
			APIVersion:        p.config.APIVersion,
			SupportedVersions: reg.ListSupported(),
			LatestVersion:     reg.Current(),
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
		StructuredContent: info,
	}, nil, nil
}

// personaName returns the persona's name, or "" when no persona applies.
func personaName(p *PersonaInfo) string {
	if p == nil {
		return ""
	}
	return p.Name
}

// sessionThreadingInstruction is the prominent header prepended to the composed
// agent instructions telling the model to thread the minted handle on every
// call. It is the model-facing complement to the machine-readable session_id
// field.
func sessionThreadingInstruction(sessionID string) string {
	return "SESSION HANDLE: This call issued you session_id \"" + sessionID + "\". " +
		"Pass it as the session_id argument on EVERY subsequent tool call. Tool calls " +
		"without it are refused (SESSION_REQUIRED). Do not call platform_info again unless " +
		"a call returns SESSION_EXPIRED.\n\n"
}

// mintSessionHandle creates an explicit session handle in the session store and
// returns it with its RFC3339 expiry. It returns empty strings (and mints
// nothing) when handles are disabled or no session store is configured, so the
// caller can omit the fields for a byte-identical legacy response.
func (p *Platform) mintSessionHandle(ctx context.Context, persona string) (id, expiresAt string) {
	store := p.sessions.SessionStore()
	if !p.config.Sessions.Handles.IsEnabled() || store == nil {
		return "", ""
	}
	userID := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		userID = pc.UserID
	}
	sess, err := session.MintHandle(ctx, store, userID, persona, p.config.Sessions.Handles.HandleTTL())
	if err != nil {
		slog.Error("platform_info: failed to mint session handle", "error", err)
		return "", ""
	}
	return sess.ID, sess.ExpiresAt.UTC().Format(time.RFC3339)
}
