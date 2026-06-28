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
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// PromptCreator creates and registers prompts at runtime.
type PromptCreator interface {
	Create(ctx context.Context, p *prompt.Prompt) error
	RegisterRuntimePrompt(p *prompt.Prompt)
}

const (
	// applyToolName is the MCP tool name for applying knowledge.
	applyToolName = "apply_knowledge"

	// idLength is the number of random bytes used to generate insight IDs.
	idLength = 16

	// promptName is the MCP prompt name for knowledge capture guidance.
	promptName = "knowledge_capture_guidance"

	// applyPromptName is the MCP prompt name for the review-and-apply guidance:
	// how to synthesize insights into durable knowledge (DataHub or knowledge pages).
	applyPromptName = "knowledge_apply_guidance"

	// userPromptName is the user-facing prompt for capturing knowledge.
	userPromptName = "capture-this-as-knowledge"
)

// applyKnowledgeInput defines the input schema for the apply_knowledge tool.
type applyKnowledgeInput struct {
	Action     string        `json:"action"`
	EntityURN  string        `json:"entity_urn,omitempty"`
	InsightIDs []string      `json:"insight_ids,omitempty"`
	Changes    []ApplyChange `json:"changes,omitempty"`
	Confirm    bool          `json:"confirm,omitempty"`
	// Sink selects the apply target: "" / "datahub" (default) applies DataHub
	// changes; "knowledge_page" promotes a capture to a canonical knowledge page.
	Sink string `json:"sink,omitempty"`
	// Page is the curated page payload for sink=knowledge_page (#633 Goal 3).
	Page *pagePromotionInput `json:"page,omitempty"`
	// For approve/reject actions
	ReviewNotes string `json:"review_notes,omitempty"`
	// ChangesetID is the target changeset for the rollback action.
	ChangesetID string `json:"changeset_id,omitempty"`
	// Itemize, with action=bulk_review, returns the pending insights themselves
	// (each a full record with id, captured_by, sink_class, status and
	// entity_urns), windowed by Offset/Limit, in addition to the aggregate
	// counts. It is how an agent enumerates the review queue; the relevance
	// ranked search tool cannot list it completely. Limit defaults to
	// DefaultLimit and is capped at MaxLimit; Offset is the page start.
	Itemize bool `json:"itemize,omitempty"`
	Limit   int  `json:"limit,omitempty"`
	Offset  int  `json:"offset,omitempty"`
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
	pageWriter          pageWriter

	// pageGuards holds the resolved knowledge-page write guards (#705): the
	// create-time duplicate gate and the oversized-page split suggestion. embeddingProv
	// computes the dedup probe's query vector; a nil/noop provider makes the gate a
	// no-op (cosine is undefined without a real embedder).
	pageGuards    knowledgepage.PageGuards
	embeddingProv embedding.Provider

	semanticProvider semantic.Provider
	queryProvider    query.Provider

	promptCreator PromptCreator
}

// SetPageGuards wires the resolved knowledge-page write-guard thresholds and the
// embedding provider used by the create-time duplicate gate (#705). A nil provider
// (or the noop placeholder) leaves the gate inactive: without a real embedding the
// cosine similarity is not defined, so the create proceeds unguarded.
func (t *Toolkit) SetPageGuards(guards knowledgepage.PageGuards, emb embedding.Provider) {
	t.pageGuards = guards
	t.embeddingProv = emb
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

// SetPromptCreator sets the prompt creator for add_prompt change type support.
func (t *Toolkit) SetPromptCreator(pc PromptCreator) {
	t.promptCreator = pc
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

// RegisterTools registers the knowledge toolkit's tools. Capture moved to the
// memory toolkit's memory_capture verb (#633) and reading is search;
// this toolkit owns admin promotion (apply_knowledge) and the capture-guidance
// prompts.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.applyEnabled {
		mcp.AddTool(s, &mcp.Tool{
			Name:  applyToolName,
			Title: "Apply Knowledge",
			Description: "The review-and-apply gate of the knowledge loop. (The name is historical; read it as 'review and apply knowledge'.) " +
				"The loop: users and agents capture memories with memory_capture; durable ones become insights pending review " +
				"(business_knowledge, schema_entity, operational_rule); holding this tool you review those insights and turn the good ones into " +
				"durable, shared, canonical KNOWLEDGE that every future session recalls via search, so no one re-teaches the same fact. " +
				"Knowledge has two homes: a DataHub entity when the fact is tied to a catalog dataset or column, or a canonical knowledge page " +
				"(sink=knowledge_page) when it is business or domain knowledge not tied to one entity (for example seasonal date ranges or company vocabulary). " +
				"Be an expert reviewer, not a mechanical one: " +
				"(1) discover existing knowledge first by searching DataHub and knowledge pages for the topic, so every decision is update-vs-create, never blind-create; " +
				"(2) compare each insight against what exists (new, a refinement, a correction, or already covered); " +
				"(3) synthesize: merge related insights into one coherent statement and resolve contradictions, rather than writing one artifact per raw insight; " +
				"(4) route: entity-tied facts update the DataHub entity (description, tags, glossary, curated queries); business or domain knowledge promotes to a knowledge page " +
				"via sink=knowledge_page with a 'page' object, found-or-created by slug so repeat promotions consolidate and references accumulate; " +
				"(5) update in place over duplicating, and create only when genuinely new: a create whose content closely matches an existing page is blocked and the candidate pages are returned, " +
				"so re-apply against a candidate's slug to update it, or set page.force_new only when the new page is genuinely distinct; " +
				"(6) prefer several focused, cross-linked pages over one large page, citing related pages with mcp:knowledge_page: references and building a thin index page that links to the focused ones; an oversized page is flagged with a non-blocking split suggestion; " +
				"(7) mark insights applied, rejected, or superseded. " +
				"Access is granted per persona by tool visibility, not by an admin role. " +
				"Sinks for the apply action: sink='datahub' (default) applies 'changes' to entity_urn; sink='knowledge_page' promotes a business_knowledge or operational_rule " +
				"insight to a page using the 'page' object {slug,title,summary,body,tags} and 'insight_ids'. " +
				"Actions: bulk_review, review, synthesize, apply, approve, reject, rollback, list_changesets. " +
				"rollback (changeset_id required, confirm required) reverts the aspects a prior apply changed, back to their before-image: " +
				"it removes tags/glossary terms/documentation links the apply added (leaving any that pre-existed) and restores the prior description. " +
				"Rollback is refused if the changeset is already rolled back, if a newer changeset has since changed the same aspect, " +
				"or if the changeset touched change types whose prior state was not captured (column descriptions, structured properties, incidents, curated queries, context documents, prompts). " +
				"list_changesets (entity_urn required) lists an entity's changesets with their ids, timestamps, actors, and rollback status. " +
				"Change types: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query, " +
				"set_structured_property, remove_structured_property, raise_incident, resolve_incident, " +
				"add_context_document, update_context_document, remove_context_document. " +
				"For update_description, use target 'column:<fieldPath>' for column-level (e.g., 'column:location_type_id'), omit for entity-level. " +
				"update_description works on datasets, dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, and glossaryNodes. " +
				"Column-level descriptions (column:<fieldPath>) are dataset-only. " +
				"add_tag, remove_tag, add_glossary_term, add_documentation, and flag_quality_issue work on all entity types. " +
				"add_curated_query is dataset-only. " +
				"For add_tag/remove_tag, detail is the tag name or URN (e.g., 'pii' or 'urn:li:tag:pii'). " +
				"flag_quality_issue adds a fixed 'QualityIssue' tag; the detail text is stored as context in the knowledge store. " +
				"For add_documentation, target is the URL, detail is the link description. " +
				"For add_curated_query, detail is the query name, query_sql is the SQL statement (required), and query_description is optional. " +
				"For set_structured_property, target is the property qualified name or URN, detail is the value or JSON array. " +
				"For remove_structured_property, target is the property qualified name or URN. " +
				"For raise_incident, target is the incident title, detail is the optional description. " +
				"For resolve_incident, target is the incident URN, detail is the resolution message. " +
				"For add_context_document, target is the document title, detail is the content, query_description is the category. " +
				"For update_context_document, target is the document ID, detail is the new content, query_sql is the new title, query_description is the category. " +
				"For remove_context_document, target is the document ID. " +
				"add_context_document/update_context_document work on datasets, glossaryTerms, glossaryNodes, and containers. " +
				"Structured properties, incidents, and context documents require DataHub 1.4.x. " +
				"Insight lifecycle: pending → approved/rejected/superseded; approved → applied/rejected; applied → rolled_back.",
			InputSchema: applyKnowledgeSchema,
		}, t.handleApplyKnowledge)
	}

	// Insight recall is served by the unified search tool (#632);
	// this toolkit no longer registers a separate recall_insight tool.

	t.registerPrompt(s)
}

// Tools returns the list of tool names provided by this toolkit.
func (t *Toolkit) Tools() []string {
	var tools []string
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

// handleApplyKnowledge dispatches to the appropriate action handler.
func (t *Toolkit) handleApplyKnowledge(ctx context.Context, _ *mcp.CallToolRequest, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if err := ValidateAction(input.Action); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	return t.dispatchApplyAction(ctx, input)
}

// dispatchApplyAction routes a validated apply_knowledge action to its handler.
func (t *Toolkit) dispatchApplyAction(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	switch input.Action {
	case actionBulkReview:
		return t.handleBulkReview(ctx, input)
	case actionReview:
		return t.handleReview(ctx, input)
	case actionSynthesize:
		return t.handleSynthesize(ctx, input)
	case actionApply:
		return t.handleApply(ctx, input)
	case actionApprove:
		return t.handleApproveReject(ctx, input, StatusApproved)
	case actionReject:
		return t.handleApproveReject(ctx, input, StatusRejected)
	case actionRollback:
		return t.handleRollback(ctx, input)
	case actionListChangesets:
		return t.handleListChangesets(ctx, input)
	default:
		return errorResult("unknown action: " + input.Action), nil, nil
	}
}

// bulkReviewScopeNote labels the bulk_review denominators so the headline
// numbers are not mistaken for one another. by_entity counts a multi-entity
// insight once per URN and omits entity-agnostic insights, so it does not sum
// to total_pending; itemize:true is the way to see every pending insight.
const bulkReviewScopeNote = "Counts are pending insights only (the global review queue). " +
	"by_entity counts an insight once per entity URN it carries and omits insights " +
	"that have no entity URN, so it does not sum to total_pending. " +
	"Pass itemize:true to enumerate every pending insight (with id, captured_by and sink_class)."

// handleBulkReview summarizes the pending review queue. The default (counts-only)
// path stays cheap and bounded; itemize:true enumerates the whole queue.
func (t *Toolkit) handleBulkReview(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.Itemize {
		return t.bulkReviewItemized(ctx, input)
	}
	return t.bulkReviewCounts(ctx)
}

// bulkReviewCounts returns the pending-queue aggregates without materializing the
// whole queue: the counts come from store.Stats (a cheap grouped count), and
// by_entity is a bounded sample of one page. The complete, itemized enumeration is
// the itemize path, so a large queue never inflates the default counts response.
func (t *Toolkit) bulkReviewCounts(ctx context.Context) (*mcp.CallToolResult, any, error) {
	stats, err := t.store.Stats(ctx, InsightFilter{Status: StatusPending})
	if err != nil {
		return errorResult("failed to get stats: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	sample, _, err := t.store.List(ctx, InsightFilter{Status: StatusPending, Limit: MaxLimit})
	if err != nil {
		return errorResult("failed to list insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	result := map[string]any{
		"total_pending": stats.TotalPending,
		"by_entity":     buildEntitySummaries(sample),
		"by_category":   stats.ByCategory,
		"by_confidence": stats.ByConfidence,
		"note":          bulkReviewScopeNote,
	}
	if stats.TotalPending > len(sample) {
		// by_entity is built from one page, so it is a sample here; itemize:true
		// returns every pending insight including entity-agnostic ones.
		result["by_entity_complete"] = false
	}
	return jsonResult(result)
}

// bulkReviewItemized returns the complete pending queue: aggregates and by_entity
// from one full walk, plus the windowed insights themselves (id, captured_by,
// sink_class, ...) so a reviewer can enumerate and act on every pending insight.
func (t *Toolkit) bulkReviewItemized(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	pending, err := t.collectPending(ctx)
	if err != nil {
		return errorResult("failed to list pending insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	byCategory := make(map[string]int)
	byConfidence := make(map[string]int)
	for i := range pending {
		byCategory[pending[i].Category]++
		byConfidence[pending[i].Confidence]++
	}

	page, offset, next := pageInsights(pending, input.Offset, input.Limit)
	result := map[string]any{
		"total_pending": len(pending),
		"by_entity":     buildEntitySummaries(pending),
		"by_category":   byCategory,
		"by_confidence": byConfidence,
		"note":          bulkReviewScopeNote,
		"insights":      page,
		"returned":      len(page),
		"offset":        offset,
	}
	if next >= 0 {
		result["next_offset"] = next
	}
	return jsonResult(result)
}

// collectPending returns every pending insight in the global review queue by
// paging List until its reported total is covered. Since #706, List filters and
// counts on the exact insight status and returns the exact pending total, so this
// loop pages the pending set directly rather than walking the coarse active set;
// for the common case (pending count <= one page) it is a single List call.
//
// The stride is MaxLimit, which is also the per-page window List returns when
// more records remain. That holds only while MaxLimit does not exceed the backing
// store's own page cap (memory.MaxLimit); if it did, pages would be short and the
// fixed stride would skip records. TestCollectPendingStrideInvariant guards that.
func (t *Toolkit) collectPending(ctx context.Context) ([]Insight, error) {
	var all []Insight
	for offset := 0; ; offset += MaxLimit {
		page, total, err := t.store.List(ctx, InsightFilter{
			Status: StatusPending,
			Limit:  MaxLimit,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("listing pending insights: %w", err)
		}
		all = append(all, page...)
		if offset+MaxLimit >= total {
			return all, nil
		}
	}
}

// pageInsights returns the window [offset, offset+limit) of insights together
// with the offset used and the next offset to request, or -1 when the window
// reaches the end. A non-positive limit defaults to DefaultLimit and is capped
// at MaxLimit; a negative offset is treated as 0.
func pageInsights(all []Insight, offset, limit int) (page []Insight, usedOffset, nextOffset int) {
	if offset < 0 {
		offset = 0
	}
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	if offset >= len(all) {
		return []Insight{}, offset, -1
	}
	end := offset + limit
	if end >= len(all) {
		return all[offset:], offset, -1
	}
	return all[offset:end], offset, end
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
		fieldEntityURN:     input.EntityURN,
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
		fieldEntityURN:      input.EntityURN,
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
	// Sink router (#633 Goal 3): non-DataHub canonical knowledge promotes to a
	// portal knowledge page; schema_entity continues to DataHub (default).
	switch input.Sink {
	case "", sinkDataHub:
		// fall through to the DataHub path below
	case sinkKnowledgePage:
		return t.promoteToPage(ctx, input)
	default:
		return errorResult("unknown sink: " + input.Sink + " (valid: datahub, knowledge_page)"), nil, nil
	}

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
			fieldEntityURN:          input.EntityURN,
			"changes_count":         len(input.Changes),
			fieldMessage:            "Set confirm: true to apply these changes.",
		})
	}

	appliedBy := authorFromContext(ctx)

	// Get current metadata for recording previous values
	prevMeta, err := t.datahubWriter.GetCurrentMetadata(ctx, input.EntityURN)
	if err != nil {
		return errorResult("failed to get current metadata: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Apply all changes atomically — collect writes first, then execute
	createdURNs, err := t.executeChanges(ctx, input.EntityURN, input.Changes)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return t.recordChangesetAndMarkApplied(ctx, input, prevMeta, appliedBy, createdURNs)
}

// recordChangesetAndMarkApplied records a changeset and marks source insights as applied.
func (t *Toolkit) recordChangesetAndMarkApplied(ctx context.Context, input applyKnowledgeInput, prevMeta *EntityMetadata, appliedBy string, createdURNs []string) (*mcp.CallToolResult, any, error) {
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
		fieldEntityURN:            input.EntityURN,
		"changes_applied":         len(input.Changes),
		"insights_marked_applied": len(input.InsightIDs),
		fieldMessage: fmt.Sprintf("Changes applied to DataHub. Roll back with action=rollback changeset_id=%s. "+
			"changes_applied counts requested changes; verify against resulting_state below.", csID),
	}

	// Re-read the entity so the caller can verify what actually persisted without
	// a follow-up call. This closes the gap where changes_applied counted requested
	// changes (a duplicate add is a no-op upstream) rather than verified writes.
	if meta, err := t.datahubWriter.GetCurrentMetadata(ctx, input.EntityURN); err == nil {
		result["resulting_state"] = metadataToMap(meta)
	}

	if len(createdURNs) > 0 {
		result["created_ids"] = createdURNs
	}

	return jsonResult(result)
}

// authorFromContext returns the acting user's identity for authorship fields
// (page created_by/created_email, changeset author, rollback author). It prefers the
// email, matching the portal page handler, captured-insight authorship, and how these
// fields are displayed; it falls back to the user id only when no email is present
// (e.g. an API-key identity), so authorship is never the opaque id when an email
// exists (#682).
func authorFromContext(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return ""
	}
	if pc.UserEmail != "" {
		return pc.UserEmail
	}
	return pc.UserID
}

// columnTargetPrefix is the prefix for column-level targets in the target field.
const columnTargetPrefix = "column:"

// executeChanges applies changes to DataHub, rolling back on failure.
// Returns a list of IDs/URNs created by changes (query URNs, document IDs, incident URNs).
// Column description updates are batched into a single read-modify-write to avoid
// the stale-read bug where back-to-back single calls lose all but the last column.
func (t *Toolkit) executeChanges(ctx context.Context, urn string, changes []ApplyChange) ([]string, error) {
	if err := validateAllChanges(urn, changes); err != nil {
		return nil, err
	}

	columnDescs, nonColumnChanges := partitionColumnChanges(changes)

	if len(columnDescs) > 0 {
		if err := t.datahubWriter.UpdateColumnDescriptionBatch(ctx, urn, columnDescs); err != nil {
			return nil, fmt.Errorf("datahub write failed for column descriptions: %w", err)
		}
	}

	return t.dispatchNonColumnChanges(ctx, urn, nonColumnChanges)
}

// validateAllChanges pre-checks entity type compatibility for all changes.
func validateAllChanges(urn string, changes []ApplyChange) error {
	for i, c := range changes {
		if err := validateEntityTypeForChange(urn, c); err != nil {
			return fmt.Errorf("change %d of %d rejected: %w", i+1, len(changes), err)
		}
	}
	return nil
}

// partitionColumnChanges separates column description updates from other changes.
func partitionColumnChanges(changes []ApplyChange) (map[string]string, []ApplyChange) {
	columnDescs := make(map[string]string)
	var other []ApplyChange
	for _, c := range changes {
		if c.ChangeType == string(actionUpdateDescription) {
			if fieldPath, ok := parseColumnTarget(c.Target); ok {
				columnDescs[fieldPath] = c.Detail
				continue
			}
		}
		other = append(other, c)
	}
	return columnDescs, other
}

// dispatchNonColumnChanges applies non-column changes individually.
func (t *Toolkit) dispatchNonColumnChanges(ctx context.Context, urn string, changes []ApplyChange) ([]string, error) {
	var createdURNs []string
	for i, c := range changes {
		queryURN, err := t.dispatchChange(ctx, urn, c)
		if err != nil {
			// Writes are not transactional: changes 1..i already persisted and are
			// NOT automatically undone. Report this honestly so the caller can use
			// rollback (or re-apply) rather than assuming a clean failure.
			if i == 0 {
				return nil, fmt.Errorf("datahub write failed for change 1 of %d: %w (no changes were applied)", len(changes), err)
			}
			return nil, fmt.Errorf("datahub write failed for change %d of %d: %w (changes 1-%d were already applied and were NOT rolled back)", i+1, len(changes), err, i)
		}
		if queryURN != "" {
			createdURNs = append(createdURNs, queryURN)
		}
	}
	return createdURNs, nil
}

// dispatchChange executes a single change against DataHub.
// Returns a non-empty ID/URN for changes that create resources (queries, documents, incidents).
func (t *Toolkit) dispatchChange(ctx context.Context, urn string, c ApplyChange) (string, error) {
	switch c.ChangeType {
	case string(actionAddCuratedQuery):
		return t.dispatchCuratedQuery(ctx, urn, c)
	case string(actionAddPrompt):
		return t.dispatchAddPrompt(ctx, c)
	default:
		return t.dispatchCoreOrV14Change(ctx, urn, c)
	}
}

// dispatchCoreOrV14Change handles core DataHub changes and delegates to V14 for newer types.
func (t *Toolkit) dispatchCoreOrV14Change(ctx context.Context, urn string, c ApplyChange) (string, error) {
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
		err = t.datahubWriter.AddDocumentationLink(ctx, urn, c.Target, c.Detail)
	case string(actionFlagQualityIssue):
		err = t.datahubWriter.AddTag(ctx, urn, qualityIssueTagURN)
	default:
		return t.dispatchV14Change(ctx, urn, c)
	}
	if err != nil {
		return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
	}
	return "", nil
}

// dispatchCuratedQuery handles add_curated_query changes.
func (t *Toolkit) dispatchCuratedQuery(ctx context.Context, urn string, c ApplyChange) (string, error) {
	queryURN, err := t.datahubWriter.CreateCuratedQuery(ctx, urn, c.Detail, c.QuerySQL, c.QueryDescription)
	if err != nil {
		return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
	}
	return queryURN, nil
}

// dispatchAddPrompt handles add_prompt changes by creating a platform prompt.
func (t *Toolkit) dispatchAddPrompt(ctx context.Context, c ApplyChange) (string, error) {
	if t.promptCreator == nil {
		return "", fmt.Errorf("prompt creation not available: feature not initialized")
	}
	desc := c.QueryDescription
	if desc == "" {
		desc = "Agent-captured prompt"
	}
	p := &prompt.Prompt{
		Name:        c.Target,
		DisplayName: c.Target,
		Description: desc,
		Content:     c.Detail,
		Arguments:   []prompt.Argument{},
		Scope:       prompt.ScopeGlobal,
		Personas:    []string{},
		Source:      prompt.SourceAgent,
		Enabled:     true,
	}
	if err := t.promptCreator.Create(ctx, p); err != nil {
		return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
	}
	t.promptCreator.RegisterRuntimePrompt(p)
	return p.ID, nil
}

// dispatchV14Change handles DataHub 1.4.x change types.
func (t *Toolkit) dispatchV14Change(ctx context.Context, urn string, c ApplyChange) (string, error) {
	var err error
	switch c.ChangeType {
	case string(actionSetStructuredProperty):
		values, parseErr := parsePropertyValues(c.Detail)
		if parseErr != nil {
			return "", fmt.Errorf("parsing property values: %w", parseErr)
		}
		err = t.datahubWriter.UpsertStructuredProperties(ctx, urn, normalizeStructuredPropertyURN(c.Target), values)
	case string(actionRemoveStructuredProperty):
		err = t.datahubWriter.RemoveStructuredProperty(ctx, urn, normalizeStructuredPropertyURN(c.Target))
	case string(actionRaiseIncident):
		incidentURN, iErr := t.datahubWriter.RaiseIncident(ctx, urn, c.Target, c.Detail)
		if iErr != nil {
			return "", fmt.Errorf(errFmtExecuting, c.ChangeType, iErr)
		}
		return incidentURN, nil
	case string(actionResolveIncident):
		err = t.datahubWriter.ResolveIncident(ctx, c.Target, c.Detail)
	default:
		return t.dispatchContextDocumentChange(ctx, urn, c)
	}
	if err != nil {
		return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
	}
	return "", nil
}

// dispatchContextDocumentChange routes context document change types, falling back to
// unsupported for anything else.
func (t *Toolkit) dispatchContextDocumentChange(ctx context.Context, urn string, c ApplyChange) (string, error) {
	switch c.ChangeType {
	case string(actionAddContextDocument), string(actionUpdateContextDocument):
		return t.dispatchContextDocumentUpsert(ctx, urn, c)
	case string(actionRemoveContextDocument):
		if err := t.datahubWriter.DeleteContextDocument(ctx, c.Target); err != nil {
			return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported change type: %s", c.ChangeType)
	}
}

// dispatchContextDocumentUpsert handles add_context_document and update_context_document.
// For add: target = title, detail = content, query_description = category.
// For update: target = document ID, detail = content, query_sql = new title, query_description = category.
func (t *Toolkit) dispatchContextDocumentUpsert(ctx context.Context, urn string, c ApplyChange) (string, error) {
	doc := types.ContextDocumentInput{
		Content:  c.Detail,
		Category: c.QueryDescription,
	}

	if c.ChangeType == string(actionAddContextDocument) {
		doc.Title = c.Target
	} else {
		doc.ID = c.Target
		doc.Title = c.QuerySQL
	}

	result, err := t.datahubWriter.UpsertContextDocument(ctx, urn, doc)
	if err != nil {
		return "", fmt.Errorf(errFmtExecuting, c.ChangeType, err)
	}

	if result != nil {
		return result.ID, nil
	}
	return "", nil
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

// errFmtExecuting is the format string for change dispatch errors.
const errFmtExecuting = "executing %s: %w"

// JSON field names used in tool responses.
const (
	fieldEntityURN   = "entity_urn"
	fieldMessage     = "message"
	fieldDescription = "description"
	fieldTarget      = "target"
	fieldDetail      = "detail"
)

// promptRoleUser is the MCP message Role value for user-authored
// prompt content. Distinct from `sourceUser` (the insight-provenance
// enum that happens to share the same string today) because the two
// namespaces are semantically unrelated and could diverge — a future
// rename of sourceUser must not silently change the prompt role.
const promptRoleUser = "user"

// qualityIssueTagURN is the single fixed DataHub tag applied by flag_quality_issue.
// Instead of encoding quality issue details into dynamic tag names (which pollutes
// the tag namespace), the detail text is stored as a knowledge insight for admin review.
const qualityIssueTagURN = "urn:li:tag:QualityIssue"

// normalizeStructuredPropertyURN ensures a property name is a full DataHub URN.
func normalizeStructuredPropertyURN(name string) string {
	if strings.HasPrefix(name, "urn:li:structuredProperty:") {
		return name
	}
	return "urn:li:structuredProperty:" + name
}

// parsePropertyValues parses the detail field into property values.
// Accepts JSON arrays (e.g., [90, "PII"]) or single values (e.g., "90").
func parsePropertyValues(detail string) ([]any, error) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil, fmt.Errorf("detail is required for structured property values")
	}

	// Try parsing as JSON array first, using UseNumber to preserve int64/float64 types
	if strings.HasPrefix(detail, "[") {
		var values []any
		dec := json.NewDecoder(strings.NewReader(detail))
		dec.UseNumber()
		if err := dec.Decode(&values); err != nil {
			return nil, fmt.Errorf("invalid JSON array: %w", err)
		}
		// Convert json.Number to int64 or float64
		for i, v := range values {
			if n, ok := v.(json.Number); ok {
				values[i] = convertJSONNumber(n)
			}
		}
		return values, nil
	}

	// Try parsing as JSON number, preserving numeric type
	var num json.Number
	if err := json.Unmarshal([]byte(detail), &num); err == nil {
		return []any{convertJSONNumber(num)}, nil
	}

	// Treat as a plain string value
	return []any{detail}, nil
}

// convertJSONNumber converts a json.Number to int64 or float64, preferring int64.
func convertJSONNumber(n json.Number) any {
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}

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
		// Wrap ErrUnsupportedEntityType with a user-friendly message.
		return wrapDescriptionError(err, urn)
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

// registerPrompt registers the knowledge capture guidance prompt
// and the user-facing capture prompt.
func (*Toolkit) registerPrompt(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        promptName,
		Description: "Guidance on when and how to capture domain knowledge insights",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role:    promptRoleUser,
					Content: &mcp.TextContent{Text: knowledgeCapturePrompt},
				},
			},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        applyPromptName,
		Description: "How to review insights and synthesize them into durable knowledge (DataHub or knowledge pages)",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role:    promptRoleUser,
					Content: &mcp.TextContent{Text: knowledgeApplyPrompt},
				},
			},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        userPromptName,
		Description: "Record insights from this conversation for data catalog improvement",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role:    promptRoleUser,
					Content: &mcp.TextContent{Text: captureKnowledgePromptContent},
				},
			},
		}, nil
	})
}

// PromptInfos returns metadata for prompts registered by the knowledge toolkit.
func (*Toolkit) PromptInfos() []registry.PromptInfo {
	return []registry.PromptInfo{
		{
			Name:        promptName,
			Description: "Guidance on when and how to capture domain knowledge insights",
			Category:    "toolkit",
			Content:     knowledgeCapturePrompt,
		},
		{
			Name:        applyPromptName,
			Description: "How to review insights and synthesize them into durable knowledge (DataHub or knowledge pages)",
			Category:    "toolkit",
			Content:     knowledgeApplyPrompt,
		},
		{
			Name:        userPromptName,
			Description: "Record insights from this conversation for data catalog improvement",
			Category:    "toolkit",
			Content:     captureKnowledgePromptContent,
		},
	}
}

const captureKnowledgePromptContent = `Capture the key insights from this conversation as domain knowledge for the data catalog.

1. Review our conversation for corrections, business context, data quality observations, or relationships discovered
2. Identify which datasets and columns the insights relate to
3. Record each insight with appropriate categorization and confidence level
4. Suggest any catalog improvements (updated descriptions, new tags, glossary terms)`

// knowledgeApplyPrompt teaches the holder of apply_knowledge the memory -> insight
// -> knowledge loop and how to be an expert reviewer: discover existing knowledge,
// compare, synthesize, route to DataHub or a knowledge page, update-or-create, and
// mark insights applied. It is registered as the knowledge_apply_guidance prompt.
const knowledgeApplyPrompt = `## Reviewing Insights into Durable Knowledge

You hold apply_knowledge, the review-and-apply gate of the platform's knowledge loop. (The tool name is historical; read it as "review and apply knowledge.") Turning raw insights into correct, non-duplicated, durable knowledge is one of the platform's essential functions. Do it as an expert editor, not a mechanical promoter.

### The loop, and why knowledge matters

- **Memory**: transient or personal working context (personal_preference, episodic_event), live immediately and scoped to one user.
- **Insight**: a durable *candidate* fact captured for review (business_knowledge, schema_entity, operational_rule), pending until you review it.
- **Knowledge**: reviewed, durable, shared, canonical truth. It lives in DataHub when tied to a catalog entity, or in a knowledge page when it is business or domain knowledge not tied to one entity. Knowledge is what every future session recalls via search, so no user ever has to teach the same fact twice and every answer is grounded in established truth.

Always discover before you apply: search existing memory, insights, and knowledge first.

### The expert review workflow

1. **Discover existing knowledge.** For each pending insight, search DataHub and knowledge pages for the topic. The decision is always update-vs-create, never blind-create.
2. **Compare.** Is the insight new, a refinement, a correction, or already covered or superseded? Reject or supersede what is redundant or wrong.
3. **Synthesize.** Merge related insights into one coherent statement and resolve contradictions. Do not produce one page or one description per raw insight.
4. **Route to the right home.**
   - Tied to a catalog entity (dataset, column, dashboard): apply to DataHub with sink=datahub, using update_description, add_tag, add_glossary_term, add_curated_query, and so on, against the entity_urn.
   - Business or domain knowledge not tied to one entity (vocabulary, seasons, policies, cross-cutting context): promote to a knowledge page with sink=knowledge_page and a 'page' object {slug, title, summary, body, tags, references}. The page is found-or-created by slug, so promoting more insights to the same slug consolidates one living page and accumulates its entity references.
5. **Cite entities so they become tracked references.** To link a page to a dataset, asset, prompt, collection, connection, or other page, either pass the reference strings in the page 'references' list (mcp:asset:<id>, mcp:connection:(kind,name), urn:li:..., and so on) or write them in the body as plain text or a markdown link. Do NOT put a reference inside backticks or a code block: code spans are treated as documentation examples and are deliberately ignored, so a backticked URN produces no reference and no link. Each reference in the 'references' list is existence-checked against the catalog before the page is written; a citation to a missing internal (mcp:) entity rejects the apply (a DataHub urn:li: reference is free text, stored as given). References in 'references' and those carried from the source insights attach with the promotion, so a rollback undoes them; a stale insight-carried reference is skipped rather than blocking the promotion. Inline body references follow the normal body reconcile (the same as editing the page).
6. **Update in place over duplicating.** Consolidate onto the existing canonical home; create only when genuinely new. The platform enforces this on create: when a new page's slug does not yet exist but its content closely matches an existing page, the apply is blocked and the candidate pages are returned. Re-apply against a candidate's slug to update it, or set page.force_new: true only after deciding the new page is genuinely distinct (a deliberate, auditable choice, separate from confirm).
7. **Close the loop.** Mark insights applied, rejected, or superseded with review notes, so the queue reflects reality.

### Structure knowledge as a navigable graph

Prefer several focused, cross-linked pages over one sprawling page (progressive revelation). Each page covers one topic and cites the related ones with mcp:knowledge_page:<id> references; a broad topic gets a thin **index page** whose body is mostly links to the focused sub-pages. When a promotion produces an oversized page, the apply response carries a non-blocking split_suggestion: split it into focused sub-pages and link them from an index. When you fetch a knowledge page, its outbound references (the pages and entities it links to) come back alongside the body, so deep-crawl deliberately: fetch the index, then follow into the branch the question needs.

### Routing examples

- "The amount column excludes returns" applies to DataHub: update_description on that dataset column.
- "We run Summer (May-Oct) and Winter (Nov-Apr) seasons for planning" becomes a knowledge page (sink=knowledge_page), for example slug retail-seasons.
- Three insights all about discount vocabulary become one knowledge page synthesized from all three, not three pages.
- A broad "retail operations" topic becomes an index page linking to focused pages (retail-seasons, return-policy, discount-vocabulary), each citing the others, not one giant page.`

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
	seen := make(map[string]bool)
	for _, c := range changes {
		seen[c.ChangeType] = true
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
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
		fieldDescription: meta.Description,
		"tags":           meta.Tags,
		"glossary_terms": meta.GlossaryTerms,
		"owners":         meta.Owners,
	}
}

func changesToMap(changes []ApplyChange) map[string]any {
	result := map[string]any{}
	for i, c := range changes {
		key := fmt.Sprintf("change_%d", i)
		entry := map[string]any{
			"change_type": c.ChangeType,
			fieldTarget:   c.Target,
			fieldDetail:   c.Detail,
		}
		if c.QuerySQL != "" {
			entry["query_sql"] = c.QuerySQL
		}
		if c.QueryDescription != "" {
			entry["query_description"] = c.QueryDescription
		}
		result[key] = entry
	}
	return result
}

// knowledgeCapturePrompt guides the AI agent on when to suggest capturing insights.
const knowledgeCapturePrompt = `## Knowledge Capture Guidance

### Default Behavior: Capture Proactively

You should capture knowledge automatically during normal conversation. Do not wait for the user to say "capture this" or "remember that." When someone tells you a fact about their data, that is knowledge. Record it.

Use memory_capture to record everything; choose the sink-class via its 'type'. business_knowledge, schema_entity, and operational_rule are reviewed by an admin before promotion to the catalog. personal_preference and episodic_event are live for you immediately and need no review.

The goal: if a user tells you something important in one session, no user should ever have to tell the platform the same thing again.

### When to Capture an Insight

Use memory_capture (type: schema_entity or business_knowledge) when the user shares domain knowledge that would improve the data catalog. Look for these signals:

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
- Capture the insight promptly while context is fresh
- Use markdown formatting when it aids clarity: backticks for ` + "`column_name`" + ` and ` + "`table_name`" + `, bullet lists for multi-point insights, fenced code blocks for SQL examples. Plain text is fine for simple observations.`

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

// Verify PromptDescriber compliance.
var _ registry.PromptDescriber = (*Toolkit)(nil)
