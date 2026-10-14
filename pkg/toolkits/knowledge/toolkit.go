package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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
	// how to synthesize insights into durable knowledge (the catalog or knowledge pages).
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
	// Sink selects the apply target: "" / "catalog" (default) writes curation
	// to the platform catalog; "knowledge_page" promotes a capture to a
	// canonical knowledge page.
	Sink string `json:"sink,omitempty"`
	// Page is the curated page payload for sink=knowledge_page (#633 Goal 3).
	Page *pagePromotionInput `json:"page,omitempty"`
	// Instructions is the curated payload for sink=agent_instructions: the rule
	// and the section of the deployment's customized instruction layer it lives
	// under (#1607).
	Instructions *instructionsPromotionInput `json:"instructions,omitempty"`
	// For approve/reject actions
	ReviewNotes string `json:"review_notes,omitempty"`
	// ChangesetID is the target changeset for the rollback action.
	ChangesetID string `json:"changeset_id,omitempty"`
	// TagURN is the tag to remove from every entity that carries it, for the
	// bulk_untag action (#726).
	TagURN string `json:"tag_urn,omitempty"`
	// Itemize, with action=bulk_review, returns the pending insights themselves
	// (toReviewItems): each is the full insight record, including the insight_text
	// body, with the uncapped suggested_actions array replaced by
	// suggested_actions_count. Carrying the body lets a review pass read every
	// insight without a per-insight fetch (#724). It is how an agent enumerates the
	// review queue; the relevance-ranked search tool cannot list it completely.
	// Limit defaults to DefaultLimit and is capped at MaxLimit; Offset is the page
	// start. A page is additionally bounded by a byte budget so the response never
	// exceeds the output token cap (see pageInsightsBudgeted), which can return
	// fewer than Limit insights and set page_size_capped on the response.
	Itemize bool `json:"itemize,omitempty"`
	Limit   int  `json:"limit,omitempty"`
	Offset  int  `json:"offset,omitempty"`
}

// ApplyConfig configures the apply_knowledge tool.
type ApplyConfig struct {
	Enabled             bool `yaml:"enabled"`
	RequireConfirmation bool `yaml:"require_confirmation"`
}

// Toolkit implements the knowledge capture toolkit.
type Toolkit struct {
	name  string
	store InsightStore

	applyEnabled        bool
	requireConfirmation bool
	changesetStore      ChangesetStore
	// curator writes catalog curation to the platform catalog (#1920). Its
	// store is nil on a deployment with no catalog, where only the knowledge
	// page and agent-instruction sinks apply.
	curator    curator
	pageWriter pageWriter

	// instructions is the deployment's customized agent-instruction layer, the
	// third apply sink (#1607). Nil leaves sink=agent_instructions unavailable.
	// toolInventory is read at promotion time to refuse a rule naming a tool the
	// deployment does not register.
	instructions  InstructionsStore
	toolInventory ToolInventory

	// pageGuards holds the resolved knowledge-page write guards (#705): the
	// create-time duplicate gate and the oversized-page split suggestion. embeddingProv
	// computes the dedup probe's query vector; a nil/noop provider makes the gate a
	// no-op (cosine is undefined without a real embedder).
	pageGuards    knowledgepage.PageGuards
	embeddingProv embedding.Provider

	// portalBaseURL is the deployment's public portal address, used to tell a
	// caller where the page a promotion wrote is read (#1696). Empty leaves the
	// address off the response.
	portalBaseURL string

	semanticProvider semantic.Provider
	queryProvider    query.Provider

	promptCreator PromptCreator

	// reach reports whether the caller's persona reaches the connection a
	// catalog reference names. Nil reaches every reference.
	reach CatalogReach
}

// CatalogReach reports whether the caller of ctx may curate the entity a
// catalog reference names: a reference naming a connection when the caller's
// persona reaches it, and one naming none always. The portal's catalog routes
// ask the same of their caller.
type CatalogReach func(ctx context.Context, reference string) bool

// SetCatalogReach installs the connection boundary curation writes respect.
func (t *Toolkit) SetCatalogReach(reach CatalogReach) {
	t.reach = reach
}

// reaches reports whether the caller may curate reference.
func (t *Toolkit) reaches(ctx context.Context, reference string) bool {
	return t.reach == nil || t.reach(ctx, reference)
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

// SetApplyConfig enables the apply_knowledge tool with its dependencies. A nil
// store leaves catalog changes refused; the other sinks still apply.
func (t *Toolkit) SetApplyConfig(cfg ApplyConfig, csStore ChangesetStore, store catalog.Store) {
	t.applyEnabled = cfg.Enabled
	t.requireConfirmation = cfg.RequireConfirmation
	if csStore != nil {
		t.changesetStore = csStore
	} else {
		t.changesetStore = NewNoopChangesetStore()
	}
	t.curator = curator{store: store}
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
		toolkit.AddTool(s, &mcp.Tool{
			Name:  applyToolName,
			Title: "Apply Knowledge",
			Description: "The review-and-apply gate of the knowledge loop. (The name is historical; read it as 'review and apply knowledge'.) " +
				"The loop: users and agents capture memories with memory_capture; durable ones become insights pending review " +
				"(business_knowledge, schema_entity, operational_rule); holding this tool you review those insights and turn the good ones into " +
				"durable, shared, canonical KNOWLEDGE that every future session recalls via search, so no one re-teaches the same fact. " +
				"Knowledge has three homes: the platform catalog when the fact is tied to a dataset, a column or a glossary definition; a canonical knowledge page " +
				"(sink=knowledge_page) when it is business or domain knowledge not tied to one entity (for example seasonal date ranges or company vocabulary); " +
				"or this deployment's customized agent instructions (sink=agent_instructions) when it is an operating rule every future session here must know before it acts. " +
				"Be an expert reviewer, not a mechanical one: " +
				"(1) discover existing knowledge first by searching the catalog and knowledge pages for the topic, so every decision is update-vs-create, never blind-create; " +
				"(2) compare each insight against what exists (new, a refinement, a correction, or already covered); " +
				"(3) synthesize: merge related insights into one coherent statement and resolve contradictions, rather than writing one record per raw insight; " +
				"(4) route: entity-tied facts update the catalog entity (description, tags, glossary terms, curated queries); business or domain knowledge promotes to a knowledge page " +
				"via sink=knowledge_page with a 'page' object, found-or-created by slug so repeat promotions consolidate and references accumulate; " +
				"(5) update in place over duplicating, and create only when genuinely new: a create whose content closely matches an existing page is blocked and the candidate pages are returned, " +
				"so re-apply against a candidate's slug to update it, or set page.force_new only when the new page is genuinely distinct; " +
				"(6) prefer several focused, cross-linked pages over one large page, citing related pages with mcp:knowledge_page: references and building a thin index page that links to the focused ones; an oversized page is flagged with a non-blocking split suggestion; " +
				"(7) mark insights applied, rejected, or superseded. " +
				"Access is granted per persona by tool visibility, not by an admin role. " +
				"Sinks for the apply action: sink='catalog' (default) writes 'changes' to the catalog entity entity_urn names; sink='knowledge_page' promotes a business_knowledge or operational_rule " +
				"insight to a page using the 'page' object {slug,title,summary,body,tags} and 'insight_ids'; sink='agent_instructions' promotes an operating rule into the " +
				"deployment's customized agent instructions using the 'instructions' object {section,body}, found-or-created by section so a repeat promotion rewrites its own " +
				"section and leaves the rest byte-identical. That layer is read by every session in its first response, so it is byte-bounded and holds hard rules only: a body " +
				"longer than the inline limit is written to a knowledge page and the section keeps one index entry pointing at it, and a promotion naming a tool this deployment " +
				"does not register is refused. Route domain knowledge to sink='knowledge_page' instead; the instruction layer should carry at most a pointer to it. " +
				"entity_urn is a catalog reference: mcp:dataset:(kind,connection,path), mcp:column:(kind,connection,path,column), mcp:glossary_term:<id>, mcp:glossary_node:<id>, " +
				"mcp:domain:<id>, mcp:data_product:<id> or mcp:tag:<name>. " +
				"Actions: bulk_review, review, synthesize, apply, approve, reject, rollback, list_changesets, bulk_untag. " +
				"The changes of one apply are written together or not at all. " +
				"bulk_untag (tag_urn required, confirm required) removes a tag from every dataset and column carrying it, up to 500 per call, recording one changeset. " +
				"rollback (changeset_id required, confirm required) restores every aspect a prior apply or bulk_untag wrote to the value it replaced, in one transaction, " +
				"and returns the changeset's source insights to the review queue as pending (insights_returned_to_review), keeping applied_by/applied_at/changeset_ref so the next reviewer sees what was already tried; " +
				"use reject to discard an insight for good. " +
				"Rollback is refused, and nothing is reverted, if the changeset is already rolled back, if an aspect it wrote has been written again since (roll that change back first), " +
				"if it created a prompt, or if it was written before the platform catalog existed. " +
				"list_changesets (entity_urn required) lists an entity's changesets with their ids, timestamps, actors, and rollback status. " +
				"Change types: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query, " +
				"set_structured_property, remove_structured_property, raise_incident, resolve_incident, " +
				"add_context_document, update_context_document, remove_context_document, delete_tag, set_custom_property, remove_custom_property. " +
				"A dataset and its columns carry every kind of curation; curated queries, incidents and context documents are dataset-only; a glossary term, glossary node, domain, " +
				"data product or tag carries a description, owners, custom properties and documentation links. " +
				"update_description, add_tag, remove_tag and add_glossary_term take target 'column:<name>' to address a column of the dataset (e.g., 'column:location_type_id'); omit it for the entity itself. " +
				"For add_tag/remove_tag, detail is the tag name (e.g., 'pii'; 'mcp:tag:pii' is read as 'pii'). " +
				"For add_glossary_term, detail is a term reference or name; a name the catalog does not hold creates the term, and the response lists it in created_ids. " +
				"flag_quality_issue adds a fixed 'QualityIssue' tag and, on a dataset with a detail, opens a 'Data quality issue' incident whose description is the detail (re-applying the same detail while it is open adds none). " +
				"For add_documentation, target is the URL, detail is the link description. " +
				"For add_curated_query, detail is the query name, query_sql is the SQL statement (required), and query_description is optional; a query of the same name is replaced. " +
				"For set_structured_property, target is the property name, detail is the value or JSON array. " +
				"For remove_structured_property, target is the property name. " +
				"For raise_incident, target is the incident title, detail is the optional description; the incident id is returned in created_ids. " +
				"For resolve_incident, target is the incident id, detail is the resolution message. " +
				"For add_context_document, target is the document title, detail is the content, query_description is the category; the document id is returned in created_ids. " +
				"For update_context_document, target is the document id, detail is the new content, query_sql is the new title, query_description is the category. " +
				"For remove_context_document, target is the document id. " +
				"delete_tag (entity_urn mcp:tag:<name>) takes the tag off every dataset and column carrying it. " +
				"update_description with entity_urn set to mcp:tag:<name> describes the tag itself. " +
				"For set_custom_property, target is the key, detail is the value; for remove_custom_property, target is the key. " +
				"Insight lifecycle: pending → approved/rejected/superseded; approved → applied/rejected; applied → pending (when its changeset is rolled back).",
			InputSchema: applyKnowledgeSchema,
			// bulk_untag and delete_tag remove, and update_description
			// overwrites what an entity already said.
			Annotations: toolkit.WriteAnnotations(true),
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
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	// One operation per call, with a span under the tool call (#1898).
	ctx, op := opsobs.Start(ctx, opsobs.OpKnowledgeApply)
	res, out, err := t.dispatchApplyAction(ctx, input)
	opsobs.EndTool(ctx, op, res, err)
	if input.Action == actionApply {
		recordApplyOutcome(ctx, input.Sink, res, err)
	}
	return res, out, err
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
	case actionBulkUntag:
		return t.handleBulkUntag(ctx, input)
	default:
		return toolkit.ErrorResult("unknown action: " + input.Action), nil, nil
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

// addStalenessRollup adds the pending-queue staleness fields to a bulk_review
// response so a reviewer (and an agent nudging one) can see how stale the review
// debt is: oldest_pending_at, oldest_pending_age_days, and pending_over_30d
// (#764). The fields are omitted for an empty queue (oldest == nil), where there
// is no debt to report.
func addStalenessRollup(result map[string]any, oldest *time.Time, over30d int, now time.Time) {
	if oldest == nil {
		return
	}
	result["oldest_pending_at"] = oldest.UTC().Format(time.RFC3339)
	result["oldest_pending_age_days"] = AgeDays(*oldest, now)
	result["pending_over_30d"] = over30d
}

// AgeDays returns the whole-day age of t relative to now, floored at 0 so a
// clock skew that puts t slightly in the future never reports negative. It is the
// single definition of whole-day insight age shared by bulk_review and
// platform_info so the two never drift (#764); the portal mirrors it in TS.
func AgeDays(t, now time.Time) int {
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

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
		return toolkit.ErrorResult("failed to get stats: " + err.Error()), nil, nil
	}
	sample, _, err := t.store.List(ctx, InsightFilter{Status: StatusPending, Limit: MaxLimit})
	if err != nil {
		return toolkit.ErrorResult("failed to list insights: " + err.Error()), nil, nil
	}

	result := map[string]any{
		"total_pending": stats.TotalPending,
		"by_entity":     buildEntitySummaries(sample),
		"by_category":   stats.ByCategory,
		"by_confidence": stats.ByConfidence,
		"note":          bulkReviewScopeNote,
	}
	addStalenessRollup(result, stats.OldestPendingAt, stats.PendingOver30d, time.Now())
	if stats.TotalPending > len(sample) {
		// by_entity is built from one page, so it is a sample here; itemize:true
		// returns every pending insight including entity-agnostic ones.
		result["by_entity_complete"] = false
	}
	return toolkit.JSONResultTyped(result)
}

// bulkReviewItemized returns the complete pending queue: aggregate counts and a
// bounded by_entity summary computed over the pending set, plus the windowed
// insights themselves projected for review so a reviewer can enumerate and act on
// every pending insight. Each part of the response is independently bounded so a large
// queue never pushes the response past the MCP tool output token limit (#724):
// the insights window is byte-budgeted (pageInsightsBudgeted), by_entity is capped
// (boundedEntitySummaries), and by_category/by_confidence are inherently small.
func (t *Toolkit) bulkReviewItemized(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	pending, err := t.collectPending(ctx)
	if err != nil {
		return toolkit.ErrorResult("failed to list pending insights: " + err.Error()), nil, nil
	}

	byCategory := make(map[string]int)
	byConfidence := make(map[string]int)
	for i := range pending {
		byCategory[pending[i].Category]++
		byConfidence[pending[i].Confidence]++
	}

	page := pageInsightsBudgeted(pending, input.Offset, input.Limit, bulkReviewItemBudgetBytes)
	entities, entitiesTruncated := boundedEntitySummaries(pending, maxBulkReviewEntitySummaries)
	now := time.Now()
	oldest, over30d := pendingStalenessFromInsights(pending, pendingStalenessCutoff(now))
	result := map[string]any{
		"total_pending": len(pending),
		"by_entity":     entities,
		"by_category":   byCategory,
		"by_confidence": byConfidence,
		"note":          bulkReviewScopeNote,
		"insights":      toReviewItems(page.insights),
		"returned":      len(page.insights),
		"offset":        page.offset,
	}
	addStalenessRollup(result, oldest, over30d, now)
	if entitiesTruncated {
		// by_entity was capped to the busiest entities so the response stays
		// bounded; the full per-insight entity_urns remain on each insight.
		result["by_entity_truncated"] = true
	}
	if page.next >= 0 {
		result["next_offset"] = page.next
	}
	if page.capped {
		// The byte budget, not the requested limit, ended this page: fewer than
		// limit insights were returned so the response stays under the output cap.
		// Page on with next_offset to read the rest (#724).
		result["page_size_capped"] = true
	}
	return toolkit.JSONResultTyped(result)
}

// bulkReviewItem is the per-insight shape returned by itemized bulk_review. It is
// the full Insight (so every field a reviewer had before this change, including
// session_id, persona and related_columns, is preserved and no per-insight fetch
// is needed, #724) with one substitution: the suggested_actions array is hidden
// and replaced by suggested_actions_count. suggested_actions carry uncapped query
// SQL, so including them inline let a single insight overflow the output token
// limit and stall paging; the full array remains available via fetch
// mcp:insight:<id>. The outer SuggestedActions field shadows the embedded one (same
// JSON name, shallower depth) so the array is omitted from the output.
type bulkReviewItem struct {
	Insight
	SuggestedActions      []SuggestedAction `json:"suggested_actions,omitempty"`
	SuggestedActionsCount int               `json:"suggested_actions_count"`
}

// toReviewItem wraps one insight as a review item: the suggested_actions array is
// dropped from the embedded copy and replaced by its count.
func toReviewItem(in Insight) bulkReviewItem {
	count := len(in.SuggestedActions)
	in.SuggestedActions = nil
	return bulkReviewItem{Insight: in, SuggestedActionsCount: count}
}

// toReviewItems maps insights to the review-item shape.
func toReviewItems(insights []Insight) []bulkReviewItem {
	items := make([]bulkReviewItem, len(insights))
	for i := range insights {
		items[i] = toReviewItem(insights[i])
	}
	return items
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

// normalizeWindow clamps a requested offset/limit against a record count, returning
// the usable start offset and the exclusive end of the limit window (end == start
// when the offset is past the data). A non-positive limit defaults to DefaultLimit
// and is capped at MaxLimit; a negative offset is treated as 0. pageInsights and
// pageInsightsBudgeted share it so the paging bounds have one definition.
func normalizeWindow(offset, limit, n int) (start, end int) {
	if offset < 0 {
		offset = 0
	}
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	if offset >= n {
		return offset, offset
	}
	return offset, min(offset+limit, n)
}

// pageInsights returns the window [offset, offset+limit) of insights together
// with the offset used and the next offset to request, or -1 when the window
// reaches the end.
func pageInsights(all []Insight, offset, limit int) (page []Insight, usedOffset, nextOffset int) {
	start, end := normalizeWindow(offset, limit, len(all))
	if start >= len(all) {
		return []Insight{}, start, -1
	}
	if end >= len(all) {
		return all[start:], start, -1
	}
	return all[start:end], start, end
}

// bulkReviewItemBudgetBytes bounds the cumulative serialized size of the insights
// returned by one itemized bulk_review page (measured by reviewItemSize against the
// emitted indented form), so a queue of large insights never pushes the response
// past the MCP tool output token limit (#724). It is chosen together with
// maxBulkReviewEntitySummaries so their joint worst case (this budget of insights
// plus that many bounded entity summaries, the only two queue-sized parts of the
// response) stays well under a ~25k-token output cap at roughly four bytes/token.
const bulkReviewItemBudgetBytes = 40000

// budgetedPage is the result of one byte-budgeted itemized bulk_review window:
// the page of insights, the offset it started at, the next offset to request
// (-1 at the end of the queue), and whether the byte budget ended the page.
type budgetedPage struct {
	insights []Insight
	offset   int
	next     int
	capped   bool
}

// pageInsightsBudgeted windows all from offset like pageInsights, but additionally
// ends the page once including the next insight would push the cumulative serialized
// size past byteBudget, so an itemized bulk_review page stays under the output cap
// regardless of individual insight sizes (#724). At least one insight is always
// returned when offset is in range, so paging always makes progress; this is safe
// because the projected item (toReviewItems) drops the only uncapped field
// (suggested_actions), leaving the body (capped at MaxInsightTextLen) the largest
// part, so no single item approaches the output cap. The result's capped field
// reports whether the byte budget, rather than the limit or the end of the queue,
// ended the page.
func pageInsightsBudgeted(all []Insight, offset, limit, byteBudget int) budgetedPage {
	start, end := normalizeWindow(offset, limit, len(all))
	if start >= len(all) {
		return budgetedPage{insights: []Insight{}, offset: start, next: -1}
	}
	size := 0
	i := start
	capped := false
	for ; i < end; i++ {
		itemSize := reviewItemSize(all[i])
		// Always include the first insight of the page so a single large insight
		// does not stall paging; stop before a later insight overflows the budget.
		if i > start && size+itemSize > byteBudget {
			capped = true
			break
		}
		size += itemSize
	}
	page := budgetedPage{insights: all[start:i], offset: start, next: i, capped: capped}
	if i >= len(all) {
		page.next = -1
	}
	return page
}

// reviewItemSize reports the serialized size of the review item for an insight,
// measured against the same indented form jsonResult emits so the byte budget
// reflects the bytes actually sent. It marshals the projected item (rather than
// estimating per field) so the budget cannot drift as the item shape changes;
// json.Marshal of the item cannot fail (no unmarshalable fields), so the error is
// intentionally ignored. The second marshal here (jsonResult re-marshals the whole
// response) is negligible against the budget it guards.
func reviewItemSize(in Insight) int {
	b, _ := json.MarshalIndent(toReviewItem(in), "", "  ")
	return len(b)
}

// maxBulkReviewEntitySummaries caps the by_entity array on an itemized bulk_review
// response. by_entity carries one entry per distinct entity URN across the whole
// pending queue, which is otherwise unbounded, so capping it (with by_entity_truncated
// signaling the cut) keeps the whole response bounded, not just the insights array.
const maxBulkReviewEntitySummaries = 100

// boundedEntitySummaries returns buildEntitySummaries ordered by descending insight
// count (ties broken by URN for a stable order) and capped at maxEntries, so the
// itemized bulk_review response stays bounded even for a queue spanning many
// distinct entities. truncated reports whether the cap dropped any entry; the
// per-insight entity_urns still carry the complete entity set.
func boundedEntitySummaries(insights []Insight, maxEntries int) (summaries []EntityInsightSummary, truncated bool) {
	all := buildEntitySummaries(insights)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].EntityURN < all[j].EntityURN
	})
	if len(all) > maxEntries {
		return all[:maxEntries], true
	}
	return all, false
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
		return toolkit.ErrorResult("entity_urn is required for review action"), nil, nil
	}

	insights, _, err := t.store.List(ctx, InsightFilter{EntityURN: input.EntityURN, Limit: MaxLimit})
	if err != nil {
		return toolkit.ErrorResult("failed to list insights: " + err.Error()), nil, nil
	}

	currentMeta := t.currentMetadata(ctx, input.EntityURN)

	result := map[string]any{
		fieldEntityURN:     input.EntityURN,
		"current_metadata": currentMeta,
		"insights":         insights,
	}

	return toolkit.JSONResultTyped(result)
}

// handleSynthesize gathers approved insights and returns a structured proposal.
func (t *Toolkit) handleSynthesize(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return toolkit.ErrorResult("entity_urn is required for synthesize action"), nil, nil
	}

	// Get approved insights for this entity
	filter := InsightFilter{
		EntityURN: input.EntityURN,
		Status:    StatusApproved,
		Limit:     MaxLimit,
	}
	insights, _, err := t.store.List(ctx, filter)
	if err != nil {
		return toolkit.ErrorResult("failed to list insights: " + err.Error()), nil, nil
	}

	// Filter by specific IDs if provided
	if len(input.InsightIDs) > 0 {
		insights = filterByIDs(insights, input.InsightIDs)
	}

	currentMeta := t.currentMetadata(ctx, input.EntityURN)

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

	return toolkit.JSONResultTyped(result)
}

// currentMetadata reads the curation the catalog holds on an entity, or nil
// when there is no catalog or it does not hold the entity: a review is still
// worth answering without it.
func (t *Toolkit) currentMetadata(ctx context.Context, entity string) *EntityMetadata {
	if t.curator.store == nil {
		return nil
	}
	ref, err := t.curator.resolveTarget(ctx, entity)
	if err != nil {
		return nil
	}
	meta, err := t.curator.metadata(ctx, ref)
	if err != nil {
		return nil
	}
	return meta
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

// dispatchAddPrompt handles add_prompt changes by creating a platform prompt.
func (t *Toolkit) dispatchAddPrompt(ctx context.Context, c ApplyChange) (string, error) {
	if t.promptCreator == nil {
		return "", errors.New("prompt creation not available: feature not initialized")
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

// errFmtExecuting is the format string for change dispatch errors.
const errFmtExecuting = "executing %s: %w"

// JSON field names used in tool responses.
const (
	fieldEntityURN   = "entity_urn"
	fieldMessage     = "message"
	fieldDescription = "description"
	fieldTarget      = "target"
	fieldDetail      = "detail"
	// fieldCreatedURNs is the changeset NewValue key holding the URNs created by an
	// apply (incidents, queries, documents), used by rollback.
	fieldCreatedURNs = "created_urns"
)

// promptRoleUser is the MCP message Role value for user-authored
// prompt content. Distinct from `sourceUser` (the insight-provenance
// enum that happens to share the same string today) because the two
// namespaces are semantically unrelated and could diverge — a future
// rename of sourceUser must not silently change the prompt role.
const promptRoleUser = "user"

// qualityIssueIncidentTitle is the fixed title of the incident opened to carry a
// flag_quality_issue's detail.
const qualityIssueIncidentTitle = "Data quality issue"

// parsePropertyValues parses the detail field into property values.
// Accepts JSON arrays (e.g., [90, "PII"]) or single values (e.g., "90").
func parsePropertyValues(detail string) ([]any, error) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil, errors.New("detail is required for structured property values")
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
		return toolkit.ErrorResult("insight_ids is required for " + input.Action + " action"), nil, nil
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
	recordReviews(ctx, targetStatus, updated)

	result := map[string]any{
		"action":  input.Action,
		"updated": updated,
		"total":   len(input.InsightIDs),
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return toolkit.JSONResultTyped(result)
}

// generateID generates a cryptographically random hex ID.
func generateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
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
		Description: "How to review insights and synthesize them into durable knowledge (the catalog or knowledge pages)",
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
			Description: "How to review insights and synthesize them into durable knowledge (the catalog or knowledge pages)",
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
// compare, synthesize, route to the catalog or a knowledge page, update-or-create, and
// mark insights applied. It is registered as the knowledge_apply_guidance prompt.
const knowledgeApplyPrompt = `## Reviewing Insights into Durable Knowledge

You hold apply_knowledge, the review-and-apply gate of the platform's knowledge loop. (The tool name is historical; read it as "review and apply knowledge.") Turning raw insights into correct, non-duplicated, durable knowledge is one of the platform's essential functions. Do it as an expert editor, not a mechanical promoter.

### The loop, and why knowledge matters

- **Memory**: transient or personal working context (personal_preference, episodic_event), live immediately and scoped to one user.
- **Insight**: a durable *candidate* fact captured for review (business_knowledge, schema_entity, operational_rule), pending until you review it.
- **Knowledge**: reviewed, durable, shared, canonical truth. It lives in the platform catalog when tied to a catalog entity, or in a knowledge page when it is business or domain knowledge not tied to one entity. Knowledge is what every future session recalls via search, so no user ever has to teach the same fact twice and every answer is grounded in established truth.

Always discover before you apply: search existing memory, insights, and knowledge first.

### The expert review workflow

1. **Discover existing knowledge.** For each pending insight, search the catalog and knowledge pages for the topic. The decision is always update-vs-create, never blind-create.
2. **Compare.** Is the insight new, a refinement, a correction, or already covered or superseded? Reject or supersede what is redundant or wrong.
3. **Synthesize.** Merge related insights into one coherent statement and resolve contradictions. Do not produce one page or one description per raw insight.
4. **Route to the right home.**
   - Tied to a catalog entity (dataset, column, glossary term): apply to the catalog (the default sink, sink=catalog), using update_description, add_tag, add_glossary_term, add_curated_query, and so on, against the entity_urn, its mcp:dataset:(kind,connection,path) reference.
   - Business or domain knowledge not tied to one entity (vocabulary, seasons, policies, cross-cutting context): promote to a knowledge page with sink=knowledge_page and a 'page' object {slug, title, summary, body, tags, references}. The page is found-or-created by slug, so promoting more insights to the same slug consolidates one living page and accumulates its entity references.
5. **Cite entities so they become tracked references.** To link a page to a dataset, asset, prompt, collection, connection, or other page, either pass the reference strings in the page 'references' list (mcp:asset:<id>, mcp:connection:(kind,name), mcp:dataset:(kind,connection,path), and so on) or write them in the body as plain text or a markdown link. Do NOT put a reference inside backticks or a code block: code spans are treated as documentation examples and are deliberately ignored, so a backticked URN produces no reference and no link. Each reference in the 'references' list is existence-checked against the catalog before the page is written; a citation to a missing entity rejects the apply. References in 'references' and those carried from the source insights attach with the promotion, so a rollback undoes them; a stale insight-carried reference is skipped rather than blocking the promotion. Inline body references follow the normal body reconcile (the same as editing the page).
6. **Update in place over duplicating.** Consolidate onto the existing canonical home; create only when genuinely new. The platform enforces this on create: when a new page's slug does not yet exist but its content closely matches an existing page, the apply is blocked and the candidate pages are returned. Re-apply against a candidate's slug to update it, or set page.force_new: true only after deciding the new page is genuinely distinct (a deliberate, auditable choice, separate from confirm).
7. **Close the loop.** Pass insight_ids on the apply call for the insights you are promoting, so they are marked applied and the resulting changeset is linked to them for traceability and rollback. An apply without insight_ids writes the changes but leaves the source insights pending, so the queue no longer reflects what is live. Reject or supersede the rest with review notes.

### Structure knowledge as a navigable graph

Prefer several focused, cross-linked pages over one sprawling page (progressive revelation). Each page covers one topic and cites the related ones with mcp:knowledge_page:<id> references; a broad topic gets a thin **index page** whose body is mostly links to the focused sub-pages. When a promotion produces an oversized page, the apply response carries a non-blocking split_suggestion: split it into focused sub-pages and link them from an index. When you fetch a knowledge page, its outbound references (the pages and entities it links to) come back alongside the body, so deep-crawl deliberately: fetch the index, then follow into the branch the question needs.

### Routing examples

- "The amount column excludes returns" applies to the catalog: update_description on that dataset with target column:amount.
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
