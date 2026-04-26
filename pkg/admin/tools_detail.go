package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// findPlatformTool looks up a tool by name in the platform-level tool list
// (platform_info, list_connections, manage_prompt — tools registered directly
// on the MCP server outside of any toolkit).
func findPlatformTool(tools []platform.ToolInfo, name string) (platform.ToolInfo, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return platform.ToolInfo{}, false
}

// toolExists returns true when a registered tool of this name is reachable
// either via a toolkit or via the platform-level tool list.
func (h *Handler) toolExists(name string) bool {
	if h.deps.ToolkitRegistry != nil && h.deps.ToolkitRegistry.GetToolkitForTool(name).Found {
		return true
	}
	_, ok := findPlatformTool(h.deps.PlatformTools, name)
	return ok
}

// toolsDetailRecentWindow controls how far back the audit aggregate
// looks. 24h matches the activity tab in the admin UI; longer windows
// would require a per-call DB scan.
const toolsDetailRecentWindow = 24 * time.Hour

// toolsDetailBreakdownLimit caps the per-tool breakdown query so a
// busy platform doesn't pay full-history cost on every detail load.
const toolsDetailBreakdownLimit = 100

// ToolDetail is the aggregating response for GET /api/v1/admin/tools/{name}.
// It joins data scattered across the registry, the description-override
// middleware, persona definitions, the gateway enrichment store, and
// the audit aggregator so the admin Tools page can render everything
// for a tool from a single round-trip.
type ToolDetail struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`

	ToolkitKind string `json:"toolkit_kind"`
	ToolkitName string `json:"toolkit_name,omitempty"`
	Connection  string `json:"connection,omitempty"`

	// JSON Schema for the tool's input parameters.
	InputSchema any `json:"input_schema,omitempty"`

	// Persona allow/deny matrix — one entry per database-managed
	// persona, with the matched pattern and source recorded.
	Personas []ToolPersonaAccess `json:"personas"`

	// HiddenByGlobalDeny is true when the platform-level tools.deny
	// list matches this tool. GlobalDenyPattern is the matching glob.
	HiddenByGlobalDeny bool   `json:"hidden_by_global_deny"`
	GlobalDenyPattern  string `json:"global_deny_pattern,omitempty"`

	// HiddenByPersona maps persona name → true for any persona where
	// this tool is denied.
	HiddenByPersona map[string]bool `json:"hidden_by_persona,omitempty"`

	// Description-override status (populated when a
	// tool.<name>.description config-entry exists).
	DescriptionOverridden bool   `json:"description_overridden"`
	OverrideAuthor        string `json:"override_author,omitempty"`

	Activity *ToolActivityAggregate `json:"activity,omitempty"`

	// Number of cross-enrichment rules attached to this tool. Only
	// meaningful for proxied tools (kind=mcp); zero for native.
	EnrichmentRuleCount int `json:"enrichment_rule_count"`
}

// ToolPersonaAccess records one persona's end-to-end decision for the tool.
//
// Allowed reflects (tool allow/deny) AND (connection allow/deny). The
// MatchedPattern / Source fields describe the tool-rule decision; when
// the tool rule allows but the connection rule denies, ConnectionAllowed
// is false and Allowed is false — surfaced separately so operators can
// see both halves of the gate.
type ToolPersonaAccess struct {
	Persona           string               `json:"persona"`
	Allowed           bool                 `json:"allowed"`
	MatchedPattern    string               `json:"matched_pattern,omitempty"`
	Source            persona.AccessSource `json:"source"`
	ConnectionAllowed bool                 `json:"connection_allowed"`
}

// ToolActivityAggregate is the per-tool audit summary surfaced on the
// Activity tab. We use the existing Breakdown(by tool_name) aggregator
// rather than a new percentile query — Count, SuccessRate, and
// AvgDurationMS are what the breakdown returns natively.
type ToolActivityAggregate struct {
	WindowSeconds int64   `json:"window_seconds"`
	CallCount     int     `json:"call_count"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// getToolDetail handles GET /api/v1/admin/tools/{name}.
//
// @Summary      Get aggregating tool detail
// @Description  Returns everything the admin Tools page needs to render a single tool: kind, toolkit, connection, schema, per-persona allow/deny matrix with matched pattern, hidden state, description-override status, recent audit aggregate, and enrichment-rule count for gateway-proxied tools.
// @Tags         Tools
// @Produce      json
// @Param        name  path  string  true  "Tool name"
// @Success      200   {object}  ToolDetail
// @Failure      404   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tools/{name} [get]
func (h *Handler) getToolDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return
	}

	if h.deps.ToolkitRegistry == nil {
		writeError(w, http.StatusServiceUnavailable, "toolkit registry not available")
		return
	}
	match := h.deps.ToolkitRegistry.GetToolkitForTool(name)
	if !match.Found {
		// Fall through to platform-level tools (platform_info,
		// list_connections, manage_prompt) — these are registered
		// directly on the MCP server and aren't owned by any toolkit.
		pt, ok := findPlatformTool(h.deps.PlatformTools, name)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("tool %q not found", name))
			return
		}
		match = registry.ToolkitMatch{Kind: pt.Kind, Found: true}
	}

	detail := ToolDetail{
		Name:        name,
		ToolkitKind: match.Kind,
		ToolkitName: match.Name,
		Connection:  match.Connection,
		// Initialize collection fields so the JSON marshaler emits []
		// rather than null — the React UI does .length on these.
		Personas: []ToolPersonaAccess{},
	}

	if h.deps.MCPServer != nil {
		h.fillToolDescriptionAndSchema(r, name, &detail)
	}
	h.fillToolPersonaMatrix(r.Context(), name, &detail)
	if h.deps.Config != nil {
		if pattern, hidden := matchGlobalDeny(h.deps.Config.ToolsDenySnapshot(), name); hidden {
			detail.HiddenByGlobalDeny = true
			detail.GlobalDenyPattern = pattern
		}
	}
	h.fillDescriptionOverride(r.Context(), name, &detail)
	h.fillToolActivity(r.Context(), name, &detail)
	h.fillEnrichmentRuleCount(r.Context(), match.Kind, match.Connection, name, &detail)

	writeJSON(w, http.StatusOK, detail)
}

// fillToolDescriptionAndSchema runs ListTools on an internal session
// and copies the matching tool's description + schema into the detail.
// Description is the post-override version because the override
// middleware runs before this returns.
func (h *Handler) fillToolDescriptionAndSchema(r *http.Request, name string, d *ToolDetail) {
	session, cleanup, err := h.connectInternalSession(r)
	if err != nil {
		return
	}
	defer cleanup()
	listResult, err := session.ListTools(r.Context(), &mcp.ListToolsParams{})
	if err != nil {
		return
	}
	for _, tool := range listResult.Tools {
		if tool.Name == name {
			d.Title = tool.Title
			d.Description = tool.Description
			d.InputSchema = tool.InputSchema
			return
		}
	}
}

// fillToolPersonaMatrix runs WhyAllowed for every database-managed
// persona and records the decision. File-only personas aren't
// surfaced through PersonaStore — the Personas page is the canonical
// view for those; this is a per-tool summary of database-side rules.
func (h *Handler) fillToolPersonaMatrix(ctx context.Context, name string, d *ToolDetail) {
	if h.deps.PersonaStore == nil {
		return
	}
	defs, err := h.deps.PersonaStore.List(ctx)
	if err != nil {
		return
	}
	filter := persona.NewToolFilter(nil)
	d.HiddenByPersona = make(map[string]bool, len(defs))
	for _, def := range defs {
		p := &persona.Persona{
			Name: def.Name,
			Tools: persona.ToolRules{
				Allow: def.ToolsAllow,
				Deny:  def.ToolsDeny,
			},
			Connections: persona.ConnectionRules{
				Allow: def.ConnsAllow,
				Deny:  def.ConnsDeny,
			},
		}
		toolDecision := filter.WhyAllowed(p, name)
		// Connection check is independent. When the tool has no
		// connection (platform-level tools), IsConnectionAllowed
		// returns true unconditionally — see filter.go.
		connectionAllowed := filter.IsConnectionAllowed(p, d.Connection)
		// End-to-end Allowed mirrors what Authorizer.IsAuthorized would
		// decide for a tools/call: tool rule AND connection rule.
		allowed := toolDecision.Allowed && connectionAllowed
		d.Personas = append(d.Personas, ToolPersonaAccess{
			Persona:           def.Name,
			Allowed:           allowed,
			MatchedPattern:    toolDecision.MatchedPattern,
			Source:            toolDecision.Source,
			ConnectionAllowed: connectionAllowed,
		})
		if !allowed {
			d.HiddenByPersona[def.Name] = true
		}
	}
	sort.Slice(d.Personas, func(i, j int) bool {
		return d.Personas[i].Persona < d.Personas[j].Persona
	})
}

// fillDescriptionOverride checks for a tool.<name>.description
// config-entry and records its presence + author for the admin UI.
// The override is already applied to d.Description by the upstream
// middleware; this just surfaces the override state.
func (h *Handler) fillDescriptionOverride(ctx context.Context, name string, d *ToolDetail) {
	if h.deps.ConfigStore == nil {
		return
	}
	entry, err := h.deps.ConfigStore.Get(ctx, toolDescriptionConfigKey(name))
	if err != nil || entry == nil {
		return
	}
	// An entry with empty value is treated by ApplyConfigEntry as a
	// deletion — the live override is gone, so don't claim it's
	// overridden in the UI just because a stale row exists.
	if entry.Value == "" {
		return
	}
	d.DescriptionOverridden = true
	d.OverrideAuthor = entry.UpdatedBy
}

// fillToolActivity queries the audit Breakdown aggregator grouped by
// tool_name within the recent window, finds the row for this tool,
// and records its count + success rate + avg duration. Failures
// degrade silently — Activity stays nil and the UI renders "no data".
func (h *Handler) fillToolActivity(ctx context.Context, name string, d *ToolDetail) {
	if h.deps.AuditMetricsQuerier == nil {
		return
	}
	now := time.Now().UTC()
	from := now.Add(-toolsDetailRecentWindow)
	rows, err := h.deps.AuditMetricsQuerier.Breakdown(ctx, audit.BreakdownFilter{
		GroupBy:   audit.BreakdownByToolName,
		Limit:     toolsDetailBreakdownLimit,
		StartTime: &from,
		EndTime:   &now,
	})
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Dimension != name {
			continue
		}
		d.Activity = &ToolActivityAggregate{
			WindowSeconds: int64(toolsDetailRecentWindow / time.Second),
			CallCount:     row.Count,
			SuccessRate:   row.SuccessRate,
			AvgDurationMs: row.AvgDurationMS,
		}
		return
	}
}

// fillEnrichmentRuleCount counts cross-enrichment rules attached to
// this tool. Only meaningful for gateway-proxied tools (kind=mcp).
func (h *Handler) fillEnrichmentRuleCount(ctx context.Context, kind, connection, toolName string, d *ToolDetail) {
	if kind != "mcp" || connection == "" || h.deps.EnrichmentStore == nil {
		return
	}
	rules, err := h.deps.EnrichmentStore.List(ctx, connection, toolName, false)
	if err != nil {
		return
	}
	d.EnrichmentRuleCount = len(rules)
}

// toolDescriptionConfigKey is the canonical config-entry key used to
// override a tool's description. Centralized so the setter, getter,
// middleware, and tools-detail handler all agree on the format.
func toolDescriptionConfigKey(toolName string) string {
	return "tool." + toolName + ".description"
}

// toolsDenyConfigKey is the canonical key for the platform-wide tool
// kill-switch. The value stored in config_entries is a JSON-encoded
// []string. Centralized so the live-config hot-reload path and the
// admin UI agree.
const toolsDenyConfigKey = "tools.deny"

// toolVisibilityRequest is the body for PUT /admin/tools/{name}/visibility.
type toolVisibilityRequest struct {
	// Hidden=true adds the tool to the global tools.deny list; false removes it.
	Hidden bool `json:"hidden" example:"true"`
}

// toolVisibilityResponse is the response after applying a toggle.
type toolVisibilityResponse struct {
	ToolName string   `json:"tool_name" example:"trino_admin_kill"`
	Hidden   bool     `json:"hidden" example:"true"`
	Deny     []string `json:"deny"`
}

// setToolVisibility handles PUT /api/v1/admin/tools/{name}/visibility.
//
// @Summary      Toggle a tool's global visibility
// @Description  Adds or removes the named tool from the platform-wide tools.deny list. The deny list controls visibility in tools/list responses; persona auth gates execution independently. Read-modify-write through the standard config_entries store, so the value is JSON-encoded as []string and persists across restarts.
// @Tags         Tools
// @Accept       json
// @Produce      json
// @Param        name  path  string                 true  "Tool name"
// @Param        body  body  toolVisibilityRequest  true  "Visibility toggle"
// @Success      200   {object}  toolVisibilityResponse
// @Failure      400   {object}  problemDetail
// @Failure      404   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tools/{name}/visibility [put]
func (h *Handler) setToolVisibility(w http.ResponseWriter, r *http.Request) {
	name, req, ok := h.parseVisibilityRequest(w, r)
	if !ok {
		return
	}

	deny, err := h.loadCurrentToolsDeny(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load current tools.deny")
		return
	}

	updated := updateDenyList(deny, name, req.Hidden)
	encoded, err := json.Marshal(updated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode tools.deny")
		return
	}

	if err := h.deps.ConfigStore.Set(r.Context(), toolsDenyConfigKey, string(encoded), extractAuthor(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save tools.deny")
		return
	}
	if h.deps.Config != nil {
		h.deps.Config.ApplyConfigEntry(toolsDenyConfigKey, string(encoded))
	}

	writeJSON(w, http.StatusOK, toolVisibilityResponse{
		ToolName: name,
		Hidden:   req.Hidden,
		Deny:     updated,
	})
}

// parseVisibilityRequest validates the request preconditions and decodes the
// body. Splitting it out keeps setToolVisibility under the cyclomatic limit.
func (h *Handler) parseVisibilityRequest(w http.ResponseWriter, r *http.Request) (string, toolVisibilityRequest, bool) {
	var req toolVisibilityRequest
	if h.deps.ConfigStore == nil {
		writeError(w, http.StatusServiceUnavailable, "config store not available")
		return "", req, false
	}
	name := r.PathValue(pathKeyName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return "", req, false
	}
	if !h.toolExists(name) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("tool %q not found", name))
		return "", req, false
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return "", req, false
	}
	return name, req, true
}

// loadCurrentToolsDeny reads the current tools.deny entry from the config
// store. Falls back to the live-config slice if no DB entry exists, so a
// platform that's never been edited still returns a sane starting list.
func (h *Handler) loadCurrentToolsDeny(ctx context.Context) ([]string, error) {
	entry, err := h.deps.ConfigStore.Get(ctx, toolsDenyConfigKey)
	if err == nil && entry != nil {
		var out []string
		if jerr := json.Unmarshal([]byte(entry.Value), &out); jerr == nil {
			return out, nil
		}
	}
	if err != nil && !errors.Is(err, configstore.ErrNotFound) {
		return nil, fmt.Errorf("loading tools.deny: %w", err)
	}
	if h.deps.Config != nil {
		return h.deps.Config.ToolsDenySnapshot(), nil
	}
	return nil, nil
}

// updateDenyList returns a new list with the tool added (hidden=true) or
// removed (hidden=false). The result is sorted and de-duplicated so the
// persisted value is stable regardless of input order.
func updateDenyList(current []string, toolName string, hidden bool) []string {
	seen := make(map[string]struct{}, len(current)+1)
	for _, v := range current {
		if v == "" {
			continue
		}
		seen[v] = struct{}{}
	}
	if hidden {
		seen[toolName] = struct{}{}
	} else {
		delete(seen, toolName)
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// matchGlobalDeny returns the first deny pattern that matches the
// tool name, or empty if none. Mirrors persona's matchPattern
// semantics (filepath.Match) so global deny and persona deny use
// identical glob behavior.
func matchGlobalDeny(deny []string, toolName string) (string, bool) {
	for _, pattern := range deny {
		if matched, err := filepath.Match(pattern, toolName); err == nil && matched {
			return pattern, true
		}
	}
	return "", false
}
