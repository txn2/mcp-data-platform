package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Sink discriminators for the apply action. The default (empty / sinkDataHub)
// keeps the historical DataHub behavior; sinkKnowledgePage promotes a capture to
// a canonical portal knowledge page (the internal-knowledge home for
// business_knowledge and operational_rule, #633 Goal 3).
const (
	sinkDataHub       = "datahub"
	sinkKnowledgePage = "knowledge_page"
)

// Page changeset markers. The changeset target_urn is free-form text (migration
// 000008), so a page promotion records "kp:<slug>" and shares the same changeset
// store / list / rollback surface as DataHub changesets.
const (
	pageTargetPrefix = "kp:"
	changeCreatePage = "create_page"
	changeUpdatePage = "update_page"
	pageFieldTitle   = "title"
	pageFieldSummary = "summary"
	pageFieldBody    = "body"
	pageFieldTags    = "tags"
	pageFieldVersion = "version"
)

// Knowledge-page promotion validation bounds. These MUST match the portal REST
// handler's limits (pkg/portal/knowledge_page_handler.go maxKnowledgePage*Len)
// so a page promoted via apply and a page edited via REST obey the same rules on
// the same store.
const (
	maxPageTitleLen   = 200
	maxPageSummaryLen = 2000
	maxPageBodyLen    = 1 << 20 // 1 MiB of markdown (byte-capped, as the REST path does)
	maxPageSlugLen    = 120
)

// pageWriter is the slice of the knowledge-page store the sink router needs:
// find-or-create by slug, create, edit (new version), and soft-delete (rollback).
// It matches portal/knowledgepage.Store; declared locally so apply_knowledge
// depends on the capability and tests can supply a fake.
type pageWriter interface {
	GetBySlug(ctx context.Context, slug string) (*knowledgepage.Page, error)
	Insert(ctx context.Context, page knowledgepage.Page) error
	Update(ctx context.Context, id string, updates knowledgepage.Update) error
	SoftDelete(ctx context.Context, id string) error
}

// PageReverter is the slice of the knowledge-page store a rollback needs: look up
// by slug, restore a prior version, or soft-delete a newly created page. It is
// the read-and-revert subset of pageWriter, satisfied by portal/knowledgepage.
// Store, and is exported so the admin REST rollback (pkg/admin) can supply it via
// RollbackDeps.Pages.
type PageReverter interface {
	GetBySlug(ctx context.Context, slug string) (*knowledgepage.Page, error)
	Update(ctx context.Context, id string, updates knowledgepage.Update) error
	SoftDelete(ctx context.Context, id string) error
}

// PageEditedError is returned when a knowledge-page promotion cannot be rolled
// back because the page was edited after the promotion (its current version
// advanced past the version the changeset produced), so reverting would clobber
// the later edit. The page sink's counterpart of RollbackConflictError.
type PageEditedError struct {
	Slug             string
	CurrentVersion   int
	ChangesetVersion int
}

// Error implements the error interface.
func (e *PageEditedError) Error() string {
	return fmt.Sprintf(
		"rollback blocked: knowledge page %q was edited after this changeset (page is at v%d, changeset produced v%d); review the page and roll back manually if needed",
		e.Slug, e.CurrentVersion, e.ChangesetVersion)
}

// pagePromotionInput is the caller-curated page payload on apply (sink=knowledge_page),
// the page-sink counterpart of the DataHub `changes` list. The caller (which has read
// the source insight) supplies the markdown body and a slug for find-or-create
// consolidation.
type pagePromotionInput struct {
	Slug    string   `json:"slug,omitempty"`
	Title   string   `json:"title,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Body    string   `json:"body,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// SetPageWriter wires the knowledge-page store so apply can promote
// business_knowledge / operational_rule captures to canonical pages. A nil store
// leaves the page sink unavailable (apply with sink=knowledge_page then errors
// rather than silently no-oping).
func (t *Toolkit) SetPageWriter(pw pageWriter) {
	t.pageWriter = pw
}

// pageSinkClasses are the sink-classes whose canonical home is the internal
// knowledge system (a knowledge page) rather than DataHub. schema_entity is
// DataHub; personal_preference and episodic_event are live personal memory and
// never reach apply.
var pageSinkClasses = map[string]bool{
	memory.SinkBusinessKnowledge: true,
	memory.SinkOperationalRule:   true,
}

// promoteToPage promotes a business_knowledge / operational_rule capture into a
// canonical knowledge page (find-or-create by slug), recording a changeset for
// audit + rollback parity with the DataHub path and marking the source insights
// applied.
func (t *Toolkit) promoteToPage(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if t.pageWriter == nil {
		return errorResult("knowledge-page promotion is not configured on this deployment"), nil, nil
	}
	page := input.Page
	if page == nil {
		return errorResult("page object (slug, title, body) is required for sink=knowledge_page"), nil, nil
	}
	if msg := validatePagePromotion(*page); msg != "" {
		return errorResult(msg), nil, nil
	}

	// Mis-routing guard: every source insight must be page-class.
	originClass, err := t.validatePageInsightClasses(ctx, input.InsightIDs)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if t.requireConfirmation && !input.Confirm {
		return jsonResult(map[string]any{
			"confirmation_required": true,
			"slug":                  page.Slug,
			fieldMessage:            "Set confirm: true to promote this knowledge to a page.",
		})
	}

	appliedBy := userIDFromContext(ctx)
	tags := tagsWithOrigin(page.Tags, originClass)
	prom, err := t.applyPagePromotion(ctx, *page, tags, appliedBy)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return t.recordPageChangesetAndMarkApplied(ctx, input, prom, appliedBy)
}

// pagePromotion holds the result of writing the page, for changeset recording.
type pagePromotion struct {
	pageID     string
	slug       string
	changeType string // changeCreatePage | changeUpdatePage
	prev       map[string]any
	next       map[string]any
}

// applyPagePromotion find-or-creates the page by slug and returns the before/after
// images for the changeset.
func (t *Toolkit) applyPagePromotion(ctx context.Context, page pagePromotionInput, tags []string, appliedBy string) (*pagePromotion, error) {
	existing, err := t.pageWriter.GetBySlug(ctx, page.Slug)
	switch {
	case err == nil:
		// Snapshot the before-image and target version BEFORE updating, so the
		// changeset records the prior content regardless of whether the store
		// returns a shared row pointer.
		producedVersion := existing.CurrentVersion + 1
		prev := pageImage(existing.Title, existing.Summary, existing.Body, existing.Tags, existing.CurrentVersion)
		// Update the existing page (consolidation): a new version is produced.
		if uErr := t.pageWriter.Update(ctx, existing.ID, knowledgepage.Update{
			Title: &page.Title, Summary: &page.Summary, Body: &page.Body, Tags: &tags,
			UpdatedBy: appliedBy, ChangeSummary: "promoted from capture",
		}); uErr != nil {
			return nil, fmt.Errorf("updating knowledge page: %w", uErr)
		}
		return &pagePromotion{
			pageID: existing.ID, slug: page.Slug, changeType: changeUpdatePage,
			prev: prev,
			next: pageImage(page.Title, page.Summary, page.Body, tags, producedVersion),
		}, nil
	case errors.Is(err, knowledgepage.ErrNotFound):
		id := knowledgepage.NewID()
		if iErr := t.pageWriter.Insert(ctx, knowledgepage.Page{
			ID: id, Slug: page.Slug, Title: page.Title, Summary: page.Summary, Body: page.Body,
			Tags: tags, CreatedBy: appliedBy, CreatedEmail: appliedBy,
		}); iErr != nil {
			return nil, fmt.Errorf("creating knowledge page: %w", iErr)
		}
		return &pagePromotion{
			pageID: id, slug: page.Slug, changeType: changeCreatePage,
			prev: map[string]any{},
			next: pageImage(page.Title, page.Summary, page.Body, tags, 1),
		}, nil
	default:
		return nil, fmt.Errorf("looking up knowledge page by slug: %w", err)
	}
}

// recordPageChangesetAndMarkApplied records the promotion changeset (target
// "kp:<slug>") and marks the source insights applied, mirroring
// recordChangesetAndMarkApplied for the DataHub path.
func (t *Toolkit) recordPageChangesetAndMarkApplied(ctx context.Context, input applyKnowledgeInput, prom *pagePromotion, appliedBy string) (*mcp.CallToolResult, any, error) {
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
		TargetURN:        pageTargetPrefix + prom.slug,
		ChangeType:       prom.changeType,
		PreviousValue:    prom.prev,
		NewValue:         prom.next,
		SourceInsightIDs: insightIDs,
		AppliedBy:        appliedBy,
	}
	if err := t.changesetStore.InsertChangeset(ctx, cs); err != nil {
		return errorResult("failed to record changeset: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	for _, insID := range insightIDs {
		if err := t.store.MarkApplied(ctx, insID, appliedBy, csID); err != nil {
			slog.Warn("knowledge: failed to mark insight applied",
				"insight_id", insID, "changeset_id", csID, "error", err)
		}
	}

	action := "updated"
	if prom.changeType == changeCreatePage {
		action = "created"
	}
	return jsonResult(map[string]any{
		"changeset_id":            csID,
		"page_id":                 prom.pageID,
		"slug":                    prom.slug,
		"action":                  action,
		"insights_marked_applied": len(insightIDs),
		fieldMessage: fmt.Sprintf("Knowledge page %s. Roll back with action=rollback changeset_id=%s.",
			action, csID),
	})
}

// validatePageInsightClasses fetches each insight and rejects any whose sink-class
// does not belong to the knowledge-page sink, returning the common origin class
// for tagging. Empty insight_ids is allowed (a curator authoring a page directly).
func (t *Toolkit) validatePageInsightClasses(ctx context.Context, insightIDs []string) (string, error) {
	origin := ""
	for _, id := range insightIDs {
		ins, err := t.store.Get(ctx, id)
		if err != nil {
			return "", fmt.Errorf("insight %s not found", id)
		}
		if !pageSinkClasses[ins.SinkClass] {
			return "", fmt.Errorf("insight %s is sink-class %q, which is not promoted to a knowledge page (schema_entity goes to DataHub)", id, ins.SinkClass)
		}
		origin = ins.SinkClass
	}
	return origin, nil
}

// validatePagePromotion checks the caller-supplied page payload.
func validatePagePromotion(p pagePromotionInput) string {
	if strings.TrimSpace(p.Slug) == "" {
		return "page.slug is required for sink=knowledge_page"
	}
	if strings.TrimSpace(p.Title) == "" {
		return "page.title is required for sink=knowledge_page"
	}
	if strings.TrimSpace(p.Body) == "" {
		return "page.body is required for sink=knowledge_page"
	}
	// Rune-based to match the REST handler (a multi-byte title must obey the same
	// character limit on both write paths).
	if utf8.RuneCountInString(p.Slug) > maxPageSlugLen {
		return fmt.Sprintf("page.slug exceeds %d characters", maxPageSlugLen)
	}
	if utf8.RuneCountInString(p.Title) > maxPageTitleLen {
		return fmt.Sprintf("page.title exceeds %d characters", maxPageTitleLen)
	}
	if utf8.RuneCountInString(p.Summary) > maxPageSummaryLen {
		return fmt.Sprintf("page.summary exceeds %d characters", maxPageSummaryLen)
	}
	if len(p.Body) > maxPageBodyLen {
		return fmt.Sprintf("page.body exceeds %d bytes", maxPageBodyLen)
	}
	return ""
}

// tagsWithOrigin appends the origin sink-class as a tag (deduped) so operational
// rules and business knowledge stay filterable on the page. A blank origin (no
// source insights) leaves tags unchanged.
func tagsWithOrigin(tags []string, origin string) []string {
	out := make([]string, 0, len(tags)+1)
	seen := make(map[string]bool, len(tags)+1)
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	if origin != "" && !seen[origin] {
		out = append(out, origin)
	}
	return out
}

// intFromMap reads an int from a JSONB-roundtripped changeset value (numbers
// decode to float64).
func intFromMap(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// strsFromMap reads a string slice from a changeset value map (JSONB arrays
// decode to []any).
func strsFromMap(m map[string]any, key string) []string {
	out := []string{}
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// pageImage renders a page before/after snapshot for a changeset value.
func pageImage(title, summary, body string, tags []string, version int) map[string]any {
	return map[string]any{
		pageFieldTitle:   title,
		pageFieldSummary: summary,
		pageFieldBody:    body,
		pageFieldTags:    tags,
		pageFieldVersion: version,
	}
}
