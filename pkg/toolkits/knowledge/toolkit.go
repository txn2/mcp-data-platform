package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// toolName is the MCP tool name for capturing insights.
	toolName = "capture_insight"

	// applyToolName is the MCP tool name for applying knowledge.
	applyToolName = "apply_knowledge"

	// idLength is the number of random bytes used to generate insight IDs.
	idLength = 16

	// promptName is the MCP prompt name for knowledge capture guidance.
	promptName = "knowledge_capture_guidance"
)

// captureInsightInput defines the input schema for the capture_insight tool.
type captureInsightInput struct {
	Category         string            `json:"category"`
	InsightText      string            `json:"insight_text"`
	Confidence       string            `json:"confidence,omitempty"`
	Source           string            `json:"source,omitempty"`
	EntityURNs       []string          `json:"entity_urns,omitempty"`
	RelatedColumns   []RelatedColumn   `json:"related_columns,omitempty"`
	SuggestedActions []SuggestedAction `json:"suggested_actions,omitempty"`
}

// captureInsightOutput is the success response.
type captureInsightOutput struct {
	InsightID string `json:"insight_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// applyKnowledgeInput defines the input schema for the apply_knowledge tool.
type applyKnowledgeInput struct {
	Action     string        `json:"action"`
	EntityURN  string        `json:"entity_urn,omitempty"`
	InsightIDs []string      `json:"insight_ids,omitempty"`
	Changes    []ApplyChange `json:"changes,omitempty"`
	Confirm    bool          `json:"confirm,omitempty"`
	// For approve/reject actions
	ReviewNotes string `json:"review_notes,omitempty"`
}

// ApplyConfig configures the apply_knowledge tool.
type ApplyConfig struct {
	Enabled             bool   `yaml:"enabled"`
	DataHubConnection   string `yaml:"datahub_connection"`
	RequireConfirmation bool   `yaml:"require_confirmation"`
}

// Toolkit implements the knowledge capture toolkit.
type Toolkit struct {
	name  string
	store InsightStore

	applyEnabled        bool
	requireConfirmation bool
	changesetStore      ChangesetStore
	datahubWriter       DataHubWriter

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// New creates a new knowledge toolkit.
// If store is nil, a no-op store is used.
func New(name string, store InsightStore) (*Toolkit, error) {
	if store == nil {
		store = NewNoopStore()
	}

	return &Toolkit{
		name:  name,
		store: store,
	}, nil
}

// SetApplyConfig enables the apply_knowledge tool with its dependencies.
func (t *Toolkit) SetApplyConfig(cfg ApplyConfig, csStore ChangesetStore, writer DataHubWriter) {
	t.applyEnabled = cfg.Enabled
	t.requireConfirmation = cfg.RequireConfirmation
	if csStore != nil {
		t.changesetStore = csStore
	} else {
		t.changesetStore = NewNoopChangesetStore()
	}
	if writer != nil {
		t.datahubWriter = writer
	} else {
		t.datahubWriter = &NoopDataHubWriter{}
	}
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return "knowledge"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name for audit logging.
func (*Toolkit) Connection() string {
	return ""
}

// RegisterTools registers the capture_insight tool with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: toolName,
		Description: "Records domain knowledge shared during a session for later admin review and catalog integration. " +
			"Use this when you discover corrections to metadata, business context about data meaning, " +
			"data quality observations, usage tips, or relationships between datasets. " +
			"Set source to 'agent_discovery' for insights you figure out yourself, or 'enrichment_gap' " +
			"to flag metadata gaps for admin attention. Defaults to 'user' for user-provided knowledge.",
		InputSchema: captureInsightSchema,
	}, t.handleCaptureInsight)

	if t.applyEnabled {
		mcp.AddTool(s, &mcp.Tool{
			Name: applyToolName,
			Description: "Reviews, synthesizes, and applies captured insights to the data catalog. Admin-only. " +
				"Actions: bulk_review, review, synthesize, apply, approve, reject. " +
				"Change types: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation. " +
				"For update_description, use target 'column:<fieldPath>' for column-level (e.g., 'column:location_type_id'), omit for dataset-level. " +
				"For add_tag/remove_tag, detail is the tag name or URN (e.g., 'pii' or 'urn:li:tag:pii'). " +
				"flag_quality_issue adds a fixed 'QualityIssue' tag; the detail text is stored as context in the knowledge store. " +
				"For add_documentation, target is the URL, detail is the link description.",
			InputSchema: applyKnowledgeSchema,
		}, t.handleApplyKnowledge)
	}

	t.registerPrompt(s)
}

// Tools returns the list of tool names provided by this toolkit.
func (t *Toolkit) Tools() []string {
	tools := []string{toolName}
	if t.applyEnabled {
		tools = append(tools, applyToolName)
	}
	return tools
}

// SetSemanticProvider sets the semantic metadata provider.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider sets the query execution provider.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// Close releases resources.
func (*Toolkit) Close() error {
	return nil
}

// handleCaptureInsight handles the capture_insight tool call.
func (t *Toolkit) handleCaptureInsight(ctx context.Context, _ *mcp.CallToolRequest, input captureInsightInput) (*mcp.CallToolResult, any, error) {
	// Validate all inputs
	if err := validateInput(input); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Read context from PlatformContext (injected by auth middleware)
	pc := middleware.GetPlatformContext(ctx)

	// Generate unique ID
	id, err := generateID()
	if err != nil {
		return errorResult("internal error generating insight ID"), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Build the insight
	insight := buildInsight(id, pc, input)

	// Persist
	if err := t.store.Insert(ctx, insight); err != nil {
		return errorResult("failed to save insight: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Return success
	return successResult(id)
}

// handleApplyKnowledge dispatches to the appropriate action handler.
func (t *Toolkit) handleApplyKnowledge(ctx context.Context, _ *mcp.CallToolRequest, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if err := ValidateAction(input.Action); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	switch input.Action {
	case "bulk_review":
		return t.handleBulkReview(ctx)
	case "review":
		return t.handleReview(ctx, input)
	case "synthesize":
		return t.handleSynthesize(ctx, input)
	case "apply":
		return t.handleApply(ctx, input)
	case "approve":
		return t.handleApproveReject(ctx, input, StatusApproved)
	case "reject":
		return t.handleApproveReject(ctx, input, StatusRejected)
	default:
		return errorResult("unknown action: " + input.Action), nil, nil
	}
}

// handleBulkReview returns a summary of all pending insights.
func (t *Toolkit) handleBulkReview(ctx context.Context) (*mcp.CallToolResult, any, error) {
	stats, err := t.store.Stats(ctx, InsightFilter{Status: StatusPending})
	if err != nil {
		return errorResult("failed to get stats: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Get pending insights grouped by entity
	insights, _, err := t.store.List(ctx, InsightFilter{Status: StatusPending, Limit: MaxLimit})
	if err != nil {
		return errorResult("failed to list insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	byEntity := buildEntitySummaries(insights)

	result := map[string]any{
		"total_pending": stats.TotalPending,
		"by_entity":     byEntity,
		"by_category":   stats.ByCategory,
		"by_confidence": stats.ByConfidence,
	}

	return jsonResult(result)
}

// buildEntitySummaries groups insights by entity URN.
func buildEntitySummaries(insights []Insight) []EntityInsightSummary {
	entityMap := make(map[string]*EntityInsightSummary)

	for _, ins := range insights {
		for _, urn := range ins.EntityURNs {
			summary, ok := entityMap[urn]
			if !ok {
				summary = &EntityInsightSummary{EntityURN: urn}
				entityMap[urn] = summary
			}
			summary.Count++
			if !containsString(summary.Categories, ins.Category) {
				summary.Categories = append(summary.Categories, ins.Category)
			}
			ts := ins.CreatedAt.Format("2006-01-02T15:04:05Z")
			if summary.LatestAt == "" || ts > summary.LatestAt {
				summary.LatestAt = ts
			}
		}
	}

	result := make([]EntityInsightSummary, 0, len(entityMap))
	for _, s := range entityMap {
		result = append(result, *s)
	}
	return result
}

// handleReview returns insights for a specific entity with current metadata.
func (t *Toolkit) handleReview(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return errorResult("entity_urn is required for review action"), nil, nil
	}

	insights, _, err := t.store.List(ctx, InsightFilter{EntityURN: input.EntityURN, Limit: MaxLimit})
	if err != nil {
		return errorResult("failed to list insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	var currentMeta *EntityMetadata
	if t.datahubWriter != nil {
		meta, metaErr := t.datahubWriter.GetCurrentMetadata(ctx, input.EntityURN)
		if metaErr == nil {
			currentMeta = meta
		}
	}

	result := map[string]any{
		"entity_urn":       input.EntityURN,
		"current_metadata": currentMeta,
		"insights":         insights,
	}

	return jsonResult(result)
}

// handleSynthesize gathers approved insights and returns a structured proposal.
func (t *Toolkit) handleSynthesize(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return errorResult("entity_urn is required for synthesize action"), nil, nil
	}

	// Get approved insights for this entity
	filter := InsightFilter{
		EntityURN: input.EntityURN,
		Status:    StatusApproved,
		Limit:     MaxLimit,
	}
	insights, _, err := t.store.List(ctx, filter)
	if err != nil {
		return errorResult("failed to list insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Filter by specific IDs if provided
	if len(input.InsightIDs) > 0 {
		insights = filterByIDs(insights, input.InsightIDs)
	}

	// Get current metadata
	var currentMeta *EntityMetadata
	if t.datahubWriter != nil {
		meta, metaErr := t.datahubWriter.GetCurrentMetadata(ctx, input.EntityURN)
		if metaErr == nil {
			currentMeta = meta
		}
	}

	// Build proposed changes from suggested actions
	proposed := buildProposedChanges(insights, currentMeta)

	result := map[string]any{
		"entity_urn":        input.EntityURN,
		"current_metadata":  currentMeta,
		"approved_insights": insights,
		"proposed_changes":  proposed,
	}

	if len(proposed) == 0 {
		result["note"] = "These insights were captured without suggested_actions. " +
			"Review the insight text above and the current metadata, then construct changes for the apply action."
	}

	return jsonResult(result)
}

// buildProposedChanges assembles proposed changes from insight suggested actions.
func buildProposedChanges(insights []Insight, meta *EntityMetadata) []ProposedChange {
	proposed := make([]ProposedChange, 0, len(insights))

	for _, ins := range insights {
		for _, sa := range ins.SuggestedActions {
			pc := ProposedChange{
				ChangeType:       sa.ActionType,
				Target:           sa.Target,
				SuggestedValue:   sa.Detail,
				SourceInsightIDs: []string{ins.ID},
			}
			if meta != nil && sa.ActionType == string(actionUpdateDescription) {
				pc.CurrentValue = meta.Description
			}
			proposed = append(proposed, pc)
		}
	}

	return proposed
}

// handleApply writes changes to DataHub and records a changeset.
func (t *Toolkit) handleApply(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return errorResult("entity_urn is required for apply action"), nil, nil
	}
	if err := ValidateApplyChanges(input.Changes); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Check confirmation requirement
	if t.requireConfirmation && !input.Confirm {
		return jsonResult(map[string]any{
			"confirmation_required": true,
			"entity_urn":            input.EntityURN,
			"changes_count":         len(input.Changes),
			"message":               "Set confirm: true to apply these changes.",
		})
	}

	appliedBy := userIDFromContext(ctx)

	// Get current metadata for recording previous values
	prevMeta, err := t.datahubWriter.GetCurrentMetadata(ctx, input.EntityURN)
	if err != nil {
		return errorResult("failed to get current metadata: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Apply all changes atomically — collect writes first, then execute
	if err := t.executeChanges(ctx, input.EntityURN, input.Changes); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return t.recordChangesetAndMarkApplied(ctx, input, prevMeta, appliedBy)
}

// recordChangesetAndMarkApplied records a changeset and marks source insights as applied.
func (t *Toolkit) recordChangesetAndMarkApplied(ctx context.Context, input applyKnowledgeInput, prevMeta *EntityMetadata, appliedBy string) (*mcp.CallToolResult, any, error) {
	csID, err := generateID()
	if err != nil {
		return errorResult("internal error generating changeset ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	insightIDs := input.InsightIDs
	if insightIDs == nil {
		insightIDs = []string{}
	}

	cs := Changeset{
		ID:               csID,
		TargetURN:        input.EntityURN,
		ChangeType:       summarizeChangeTypes(input.Changes),
		PreviousValue:    metadataToMap(prevMeta),
		NewValue:         changesToMap(input.Changes),
		SourceInsightIDs: insightIDs,
		AppliedBy:        appliedBy,
	}

	if err := t.changesetStore.InsertChangeset(ctx, cs); err != nil {
		return errorResult("failed to record changeset: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Mark source insights as applied
	for _, insID := range input.InsightIDs {
		if err := t.store.MarkApplied(ctx, insID, appliedBy, csID); err != nil {
			slog.Warn("knowledge: failed to mark insight applied",
				"insight_id", insID, "changeset_id", csID, "error", err)
		}
	}

	result := map[string]any{
		"changeset_id":            csID,
		"entity_urn":              input.EntityURN,
		"changes_applied":         len(input.Changes),
		"insights_marked_applied": len(input.InsightIDs),
		"message":                 fmt.Sprintf("Changes applied to DataHub. Changeset %s recorded for rollback.", csID),
	}

	return jsonResult(result)
}

// userIDFromContext extracts the user ID from the platform context, or returns empty.
func userIDFromContext(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil {
		return pc.UserID
	}
	return ""
}

// columnTargetPrefix is the prefix for column-level targets in the target field.
const columnTargetPrefix = "column:"

// executeChanges applies changes to DataHub, rolling back on failure.
func (t *Toolkit) executeChanges(ctx context.Context, urn string, changes []ApplyChange) error {
	for i, c := range changes {
		var err error
		switch c.ChangeType {
		case string(actionUpdateDescription):
			err = t.executeUpdateDescription(ctx, urn, c)
		case string(actionAddTag):
			err = t.datahubWriter.AddTag(ctx, urn, normalizeTagURN(c.Detail))
		case string(actionRemoveTag):
			err = t.datahubWriter.RemoveTag(ctx, urn, normalizeTagURN(c.Detail))
		case string(actionAddGlossaryTerm):
			err = t.datahubWriter.AddGlossaryTerm(ctx, urn, normalizeGlossaryTermURN(c.Detail))
		case string(actionAddDocumentation):
			// detail=description, target=URL (consistent with other change types
			// where detail is always the content)
			err = t.datahubWriter.AddDocumentationLink(ctx, urn, c.Target, c.Detail)
		case string(actionFlagQualityIssue):
			err = t.datahubWriter.AddTag(ctx, urn, qualityIssueTagURN)
		}
		if err != nil {
			return fmt.Errorf("datahub write failed for change %d of %d: %w, no changes were applied", i+1, len(changes), err)
		}
	}
	return nil
}

// normalizeTagURN ensures a tag value is a full DataHub TagUrn.
// Per TagAssociation.pdl, the tag field must be a TagUrn (urn:li:tag:<name>).
func normalizeTagURN(tag string) string {
	if strings.HasPrefix(tag, "urn:li:tag:") {
		return tag
	}
	return "urn:li:tag:" + tag
}

// normalizeGlossaryTermURN ensures a glossary term value is a full DataHub GlossaryTermUrn.
// Per GlossaryTermAssociation.pdl, the urn field must be a GlossaryTermUrn.
func normalizeGlossaryTermURN(term string) string {
	if strings.HasPrefix(term, "urn:li:glossaryTerm:") {
		return term
	}
	return "urn:li:glossaryTerm:" + term
}

// qualityIssueTagURN is the single fixed DataHub tag applied by flag_quality_issue.
// Instead of encoding quality issue details into dynamic tag names (which pollutes
// the tag namespace), the detail text is stored as a knowledge insight for admin review.
const qualityIssueTagURN = "urn:li:tag:QualityIssue"

// executeUpdateDescription routes description updates to dataset-level or column-level
// based on the target field. A target of "column:<fieldPath>" routes to column description.
func (t *Toolkit) executeUpdateDescription(ctx context.Context, urn string, c ApplyChange) error {
	if fieldPath, ok := parseColumnTarget(c.Target); ok {
		if err := t.datahubWriter.UpdateColumnDescription(ctx, urn, fieldPath, c.Detail); err != nil {
			return fmt.Errorf("column description update: %w", err)
		}
		return nil
	}
	if err := t.datahubWriter.UpdateDescription(ctx, urn, c.Detail); err != nil {
		return fmt.Errorf("description update: %w", err)
	}
	return nil
}

// parseColumnTarget checks if a target string has the "column:" prefix and returns the field path.
func parseColumnTarget(target string) (string, bool) {
	if strings.HasPrefix(target, columnTargetPrefix) {
		fieldPath := target[len(columnTargetPrefix):]
		if fieldPath != "" {
			return fieldPath, true
		}
	}
	return "", false
}

// handleApproveReject transitions insight statuses.
func (t *Toolkit) handleApproveReject(ctx context.Context, input applyKnowledgeInput, targetStatus string) (*mcp.CallToolResult, any, error) {
	if len(input.InsightIDs) == 0 {
		return errorResult("insight_ids is required for " + input.Action + " action"), nil, nil
	}

	pc := middleware.GetPlatformContext(ctx)
	reviewedBy := ""
	if pc != nil {
		reviewedBy = pc.UserID
	}

	var updated int
	var errors []string
	for _, id := range input.InsightIDs {
		// Get current insight to validate transition
		insight, err := t.store.Get(ctx, id)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: not found", id))
			continue
		}
		if err := ValidateStatusTransition(insight.Status, targetStatus); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", id, err.Error()))
			continue
		}
		if err := t.store.UpdateStatus(ctx, id, targetStatus, reviewedBy, input.ReviewNotes); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", id, err.Error()))
			continue
		}
		updated++
	}

	result := map[string]any{
		"action":  input.Action,
		"updated": updated,
		"total":   len(input.InsightIDs),
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return jsonResult(result)
}

// validateInput validates all input fields.
func validateInput(input captureInsightInput) error {
	if err := ValidateCategory(input.Category); err != nil {
		return err
	}
	if err := ValidateInsightText(input.InsightText); err != nil {
		return err
	}
	if err := ValidateConfidence(input.Confidence); err != nil {
		return err
	}
	if err := ValidateSource(input.Source); err != nil {
		return err
	}
	if err := ValidateEntityURNs(input.EntityURNs); err != nil {
		return err
	}
	if err := ValidateRelatedColumns(input.RelatedColumns); err != nil {
		return err
	}
	return ValidateSuggestedActions(input.SuggestedActions)
}

// buildInsight constructs an Insight from the validated input and platform context.
func buildInsight(id string, pc *middleware.PlatformContext, input captureInsightInput) Insight {
	insight := Insight{
		ID:               id,
		Source:           NormalizeSource(input.Source),
		Category:         input.Category,
		InsightText:      input.InsightText,
		Confidence:       NormalizeConfidence(input.Confidence),
		EntityURNs:       input.EntityURNs,
		RelatedColumns:   input.RelatedColumns,
		SuggestedActions: input.SuggestedActions,
		Status:           "pending",
	}

	// Ensure slices are non-nil for JSON serialization
	if insight.EntityURNs == nil {
		insight.EntityURNs = []string{}
	}
	if insight.RelatedColumns == nil {
		insight.RelatedColumns = []RelatedColumn{}
	}
	if insight.SuggestedActions == nil {
		insight.SuggestedActions = []SuggestedAction{}
	}

	// Inject context from PlatformContext
	if pc != nil {
		insight.SessionID = pc.SessionID
		insight.CapturedBy = pc.UserID
		insight.Persona = pc.PersonaName
	}

	return insight
}

// generateID generates a cryptographically random hex ID.
func generateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// errorResult creates an error CallToolResult.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(`{"error": %q}`, msg)},
		},
		IsError: true,
	}
}

// successResult creates a success CallToolResult.
func successResult(insightID string) (*mcp.CallToolResult, any, error) {
	output := captureInsightOutput{
		InsightID: insightID,
		Status:    "pending",
		Message:   "Insight captured. It will be reviewed by a data catalog administrator.",
	}

	data, err := json.Marshal(output)
	if err != nil {
		return errorResult("internal error marshaling response"), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}

// jsonResult marshals a value to JSON and returns it as a CallToolResult.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult("internal error marshaling response"), nil, nil //nolint:nilerr // MCP protocol
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}

// registerPrompt registers the knowledge capture guidance prompt.
func (*Toolkit) registerPrompt(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        promptName,
		Description: "Guidance on when and how to capture domain knowledge insights",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: knowledgeCapturePrompt},
				},
			},
		}, nil
	})
}

// Helper functions

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

func filterByIDs(insights []Insight, ids []string) []Insight {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var result []Insight
	for _, ins := range insights {
		if idSet[ins.ID] {
			result = append(result, ins)
		}
	}
	return result
}

func summarizeChangeTypes(changes []ApplyChange) string {
	types := make(map[string]bool)
	for _, c := range changes {
		types[c.ChangeType] = true
	}
	result := make([]string, 0, len(types))
	for t := range types {
		result = append(result, t)
	}
	if len(result) == 1 {
		return result[0]
	}
	return "multiple"
}

func metadataToMap(meta *EntityMetadata) map[string]any {
	if meta == nil {
		return map[string]any{}
	}
	return map[string]any{
		"description":    meta.Description,
		"tags":           meta.Tags,
		"glossary_terms": meta.GlossaryTerms,
		"owners":         meta.Owners,
	}
}

func changesToMap(changes []ApplyChange) map[string]any {
	result := map[string]any{}
	for i, c := range changes {
		key := fmt.Sprintf("change_%d", i)
		result[key] = map[string]any{
			"change_type": c.ChangeType,
			"target":      c.Target,
			"detail":      c.Detail,
		}
	}
	return result
}

// knowledgeCapturePrompt guides the AI agent on when to suggest capturing insights.
const knowledgeCapturePrompt = `## Knowledge Capture Guidance

### When to Capture an Insight

Use the capture_insight tool when the user shares domain knowledge that would improve the data catalog. Look for these signals:

**Corrections**: The user corrects a column description, table purpose, or data interpretation.
Example: "That column isn't actually revenue, it's gross margin before returns."

**Business Context**: The user explains what data means in business terms not captured in metadata.
Example: "We only count active subscriptions, not trial accounts, for MRR calculations."

**Data Quality**: The user reports data quality issues or known limitations.
Example: "The timestamps before March 2024 are in UTC but after that they switched to America/Chicago."

**Usage Guidance**: The user shares tips on how to query or interpret data correctly.
Example: "Always filter by status='active' on that table, otherwise you get duplicates from soft deletes."

**Relationships**: The user explains connections between datasets not captured in lineage.
Example: "The customer_id in orders joins to the legacy CRM export, not the new identity table."

**Enhancements**: The user suggests improvements to existing documentation or metadata.
Example: "It would help if the sales_daily table had a tag indicating it refreshes at 6 AM CT."

### Agent-Discovered Insights

You can also capture insights you discover independently during data exploration. Set the source field to distinguish these from user-provided knowledge:

**source: "agent_discovery"** — Use when you figure something out yourself during exploration:
- Discovering what a column actually contains by sampling data (e.g., "column 'amt' appears to be in cents, not dollars, based on value ranges")
- Finding join relationships not documented in lineage (e.g., "orders.cust_id matches customers.legacy_id, not customers.id")
- Identifying data quality patterns through queries (e.g., "ship_date is NULL for 23% of completed orders since 2024-06")
- Determining refresh cadence by observing max timestamps across multiple queries

**source: "enrichment_gap"** — Use when flagging metadata gaps that need admin attention:
- A table has no description and you cannot determine its purpose from the data alone
- Column descriptions are missing or clearly outdated
- Lineage is incomplete or contradicts what the data shows
- Tags or glossary terms are absent for datasets that clearly belong to a domain

**source: "user"** (default) — Use for insights the user explicitly shares with you.

### When to Ask the User Instead

Do NOT guess when:
- Enrichment metadata is insufficient and you cannot resolve the meaning from the data alone
- Multiple interpretations of a column or table are equally plausible
- The insight would have high impact if wrong (e.g., PII classification, deprecation status, compliance tagging)

In these cases, ask the user to clarify before capturing anything.

### When NOT to Capture

Do NOT capture insights for:
- Transient questions or debugging ("why is my query slow?")
- Personal preferences ("I prefer using CTEs")
- Information already present in the catalog metadata
- Vague or unverifiable claims without specific context
- Trivial observations that don't add catalog value
- Trivially obvious metadata gaps without adding what the data actually means
- Speculative interpretations you have not verified by querying the data
- The same gap repeatedly within a single session

### Best Practices

- Include specific entity URNs when the insight relates to known datasets
- Suggest concrete actions (add_tag, update_description) when applicable
- Set confidence to "high" only when the user is clearly authoritative
- For agent discoveries, set confidence based on evidence strength: "high" if verified by querying, "medium" if inferred from patterns, "low" if speculative
- Capture the insight promptly while context is fresh`

// Verify interface compliance.
var _ interface {
	Kind() string
	Name() string
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
