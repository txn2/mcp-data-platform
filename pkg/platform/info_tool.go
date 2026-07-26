// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	personapkg "github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/session"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Info contains information about the platform deployment.
type Info struct {
	Name                string                `json:"name"`
	Version             string                `json:"version"`
	Description         string                `json:"description,omitempty"`
	Tags                []string              `json:"tags,omitempty"`
	SessionID           string                `json:"session_id,omitempty"`
	SessionExpiresAt    string                `json:"session_expires_at,omitempty"`
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
	Enabled           bool             `json:"enabled"`
	DataHubConnection string           `json:"datahub_connection,omitempty"`
	ReviewQueue       *ReviewQueueInfo `json:"review_queue,omitempty"`
}

// ReviewQueueInfo summarizes the pending apply_knowledge review queue so an agent
// can nudge a reviewer about aging review debt, e.g. "6 insights pending review,
// oldest 94 days" (#764). It is present only for a caller who can reach
// apply_knowledge and only when the queue is non-empty.
type ReviewQueueInfo struct {
	Pending              int `json:"pending"`
	OldestPendingAgeDays int `json:"oldest_pending_age_days,omitempty"`
	PendingOver30d       int `json:"pending_over_30d,omitempty"`
}

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
			ReviewQueue:       p.reviewQueueInfo(ctx),
		}
	}

	return f
}

// reviewQueueInfo summarizes the pending review queue for platform_info so an
// agent can nudge a reviewer about aging review debt (#764). It returns nil when
// knowledge is disabled (no insight store), when the stats lookup fails, or when
// the queue is empty; a failed lookup must not fail orientation, so the error is
// logged and swallowed rather than propagated.
func (p *Platform) reviewQueueInfo(ctx context.Context) *ReviewQueueInfo {
	store := p.knowledge.InsightStore()
	if store == nil {
		return nil
	}
	// Prefer the store's cheap pending-count + staleness path over the full Stats
	// fan-out: platform_info runs once per session and needs only the review-debt
	// nudge, not the category/confidence group-bys (#764).
	review, err := knowledgekit.PendingReviewOf(ctx, store)
	if err != nil {
		slog.WarnContext(ctx, "platform_info: pending review queue stats unavailable", "error", err)
		return nil
	}
	if review.TotalPending == 0 {
		return nil
	}
	info := &ReviewQueueInfo{
		Pending:        review.TotalPending,
		PendingOver30d: review.PendingOver30d,
	}
	if review.OldestPendingAt != nil {
		info.OldestPendingAgeDays = knowledgekit.AgeDays(*review.OldestPendingAt, time.Now())
	}
	return info
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
	description := p.config.Server.Description
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
	accessibleTools := instructions.AccessibleTools(p.toolkitRegistry.AllTools(), caller, p.personaRegistry)

	// Compose the full instruction stack: the platform baseline (gated to the
	// tools this caller may reach) beneath the admin business context, with the
	// resources positioning and operating rule appended as a runtime note when
	// managed resources exist.
	var notes []string
	if p.resources.Store() != nil {
		notes = append(notes, instructions.ResourcesNote(accessibleTools))
	}
	agentInstructions := instructions.ComposeForCaller(
		p.config.Server.AgentInstructions,
		p.toolkitRegistry.AllTools(),
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

	reg := DefaultRegistry()
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
		Prompts:             p.AllPromptInfos(),
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
