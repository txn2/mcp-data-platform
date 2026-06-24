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
// PageTargetURN is the changeset target_urn for a knowledge page, keyed by slug.
// Exported so other packages (the portal lineage endpoint) reference the same
// format instead of duplicating the "kp:" prefix.
func PageTargetURN(slug string) string { return pageTargetPrefix + slug }

const (
	pageTargetPrefix    = "kp:"
	changeCreatePage    = "create_page"
	changeUpdatePage    = "update_page"
	pageFieldTitle      = "title"
	pageFieldSummary    = "summary"
	pageFieldBody       = "body"
	pageFieldTags       = "tags"
	pageFieldVersion    = "version"
	pageFieldEntityURNs = "entity_urns"
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
	// Entity references (#664): carry a promoted insight's references onto the page.
	ListEntityRefs(ctx context.Context, pageID string) ([]knowledgepage.EntityRef, error)
	AddEntityRefs(ctx context.Context, pageID string, refs []knowledgepage.EntityRef) error
	ReplaceEntityRefs(ctx context.Context, pageID string, refs []knowledgepage.EntityRef) error
	ReplaceEntityRefsBySource(ctx context.Context, pageID, source string, refs []knowledgepage.EntityRef) error
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
	// Entity references (#664): restore the page's promoted references on rollback,
	// scoped to source=promoted so manual and inline references are not clobbered.
	ReplaceEntityRefsBySource(ctx context.Context, pageID, source string, refs []knowledgepage.EntityRef) error
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

	// Mis-routing guard: every source insight must be page-class. This also
	// collects the references the source insights carried, so they survive
	// promotion onto the page instead of being dropped (#664).
	originClass, entityURNs, err := t.validatePageInsightClasses(ctx, input.InsightIDs)
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
	prom, err := t.applyPagePromotion(ctx, *page, tags, appliedBy, entityURNs)
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

// applyPagePromotion find-or-creates the page by slug, carries the source
// insights' references onto it (union), and returns the before/after images for
// the changeset. entityURNs are the serialized references (any type) gathered
// from the source insights (#664).
//
// Atomicity: the page write, the reference write, and the changeset record are
// separate operations (the page and changeset already were before #664; the
// reference write joins that sequence). A failure between them can leave the
// page written while the promotion reports failure. The reference write uses
// ON CONFLICT DO NOTHING, so it is idempotent and a retry is safe; a single
// cross-store transaction spanning the page, references, and changeset is a
// broader change tracked separately.
func (t *Toolkit) applyPagePromotion(ctx context.Context, page pagePromotionInput, tags []string, appliedBy string, entityURNs []string) (*pagePromotion, error) {
	existing, err := t.pageWriter.GetBySlug(ctx, page.Slug)
	switch {
	case err == nil:
		// Snapshot the before-image and target version BEFORE updating, so the
		// changeset records the prior content regardless of whether the store
		// returns a shared row pointer.
		producedVersion := existing.CurrentVersion + 1
		prevURNs, err := t.pageEntityURNs(ctx, existing.ID)
		if err != nil {
			return nil, err
		}
		prev := pageImageWithRefs(pageSnapshot{existing.Title, existing.Summary, existing.Body, existing.Tags, existing.CurrentVersion, prevURNs})
		// Update the existing page (consolidation): a new version is produced.
		if uErr := t.pageWriter.Update(ctx, existing.ID, knowledgepage.Update{
			Title: &page.Title, Summary: &page.Summary, Body: &page.Body, Tags: &tags,
			UpdatedBy: appliedBy, ChangeSummary: "promoted from capture",
		}); uErr != nil {
			return nil, fmt.Errorf("updating knowledge page: %w", uErr)
		}
		nextURNs, err := t.addPageEntityRefs(ctx, existing.ID, entityURNs, appliedBy)
		if err != nil {
			return nil, err
		}
		// Reconcile the body's inline references (#678): a page promoted through
		// apply_knowledge gets the same source=inline refs the portal save path
		// derives, so inline mcp:/urn: links in the body become references.
		if err := t.reconcileInlineRefs(ctx, existing.ID, page.Body); err != nil {
			return nil, err
		}
		return &pagePromotion{
			pageID: existing.ID, slug: page.Slug, changeType: changeUpdatePage,
			prev: prev,
			next: pageImageWithRefs(pageSnapshot{page.Title, page.Summary, page.Body, tags, producedVersion, nextURNs}),
		}, nil
	case errors.Is(err, knowledgepage.ErrNotFound):
		id := knowledgepage.NewID()
		if iErr := t.pageWriter.Insert(ctx, knowledgepage.Page{
			ID: id, Slug: page.Slug, Title: page.Title, Summary: page.Summary, Body: page.Body,
			Tags: tags, CreatedBy: appliedBy, CreatedEmail: appliedBy,
		}); iErr != nil {
			return nil, fmt.Errorf("creating knowledge page: %w", iErr)
		}
		nextURNs, err := t.addPageEntityRefs(ctx, id, entityURNs, appliedBy)
		if err != nil {
			return nil, err
		}
		if err := t.reconcileInlineRefs(ctx, id, page.Body); err != nil {
			return nil, err
		}
		return &pagePromotion{
			pageID: id, slug: page.Slug, changeType: changeCreatePage,
			prev: pageImageWithRefs(pageSnapshot{}),
			next: pageImageWithRefs(pageSnapshot{title: page.Title, summary: page.Summary, body: page.Body, tags: tags, version: 1, entityURNs: nextURNs}),
		}, nil
	default:
		return nil, fmt.Errorf("looking up knowledge page by slug: %w", err)
	}
}

// pageEntityURNs returns the serialized URNs of the page's promoted references
// (every type, not just DataHub), for the changeset before/after image.
func (t *Toolkit) pageEntityURNs(ctx context.Context, pageID string) ([]string, error) {
	refs, err := t.pageWriter.ListEntityRefs(ctx, pageID)
	if err != nil {
		return nil, fmt.Errorf("listing page references: %w", err)
	}
	var urns []string
	for _, r := range refs {
		if r.Source == knowledgepage.RefSourcePromoted {
			urns = append(urns, r.URN())
		}
	}
	return urns, nil
}

// addPageEntityRefs unions the given references (serialized as URNs of any type)
// onto the page as promoted references and returns the page's full promoted URN
// set afterward (for the changeset after-image).
func (t *Toolkit) addPageEntityRefs(ctx context.Context, pageID string, entityURNs []string, appliedBy string) ([]string, error) {
	if len(entityURNs) > 0 {
		refs := promotedRefsFromURNs(entityURNs)
		for i := range refs {
			refs[i].CreatedBy = appliedBy
		}
		if err := t.pageWriter.AddEntityRefs(ctx, pageID, refs); err != nil {
			return nil, fmt.Errorf("adding page references: %w", err)
		}
	}
	return t.pageEntityURNs(ctx, pageID)
}

// reconcileInlineRefs scans a page body for inline mcp:/urn: references and
// replaces the page's source=inline references to match, identical to the portal
// save path (#678). Promoted and manual references are untouched. This makes a page
// authored or promoted through apply_knowledge capture the references in its body,
// not just the ones carried from the source insight.
func (t *Toolkit) reconcileInlineRefs(ctx context.Context, pageID, body string) error {
	inline := knowledgepage.ScanBodyRefs(body)
	if err := t.pageWriter.ReplaceEntityRefsBySource(ctx, pageID, knowledgepage.RefSourceInline, inline); err != nil {
		return fmt.Errorf("reconciling inline page references: %w", err)
	}
	return nil
}

// promotedRefsFromURNs parses serialized reference URNs (any type: a urn:li:
// DataHub URN or an mcp: internal reference) into promoted EntityRefs. An
// unparseable URN is skipped. This carries every reference type an insight holds
// onto the page, not just DataHub URNs (#664).
func promotedRefsFromURNs(urns []string) []knowledgepage.EntityRef {
	refs := make([]knowledgepage.EntityRef, 0, len(urns))
	for _, urn := range urns {
		ref, err := knowledgepage.ParseEntityRef(urn)
		if err != nil {
			continue
		}
		ref.Source = knowledgepage.RefSourcePromoted
		refs = append(refs, ref)
	}
	return refs
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
func (t *Toolkit) validatePageInsightClasses(ctx context.Context, insightIDs []string) (origin string, entityURNs []string, err error) {
	seen := map[string]struct{}{}
	for _, id := range insightIDs {
		ins, gErr := t.store.Get(ctx, id)
		if gErr != nil {
			return "", nil, fmt.Errorf("insight %s not found", id)
		}
		if !pageSinkClasses[ins.SinkClass] {
			return "", nil, fmt.Errorf("insight %s is sink-class %q, which is not promoted to a knowledge page (schema_entity goes to DataHub)", id, ins.SinkClass)
		}
		origin = ins.SinkClass
		// Collect the references the insight carried so they survive promotion
		// onto the page (#664). De-duped across all source insights.
		for _, urn := range insightReferenceURNs(ins) {
			if urn == "" {
				continue
			}
			if _, dup := seen[urn]; dup {
				continue
			}
			seen[urn] = struct{}{}
			entityURNs = append(entityURNs, urn)
		}
	}
	return origin, entityURNs, nil
}

// insightReferenceURNs returns the serialized reference URNs an insight carries:
// its DataHub entity_urns plus the mcp:/urn:li: references mentioned inline in its
// text. These are carried onto the page on promotion (and by the backfill), so an
// insight's references of every type survive synthesis into a knowledge page.
func insightReferenceURNs(ins *Insight) []string {
	urns := make([]string, 0, len(ins.EntityURNs))
	urns = append(urns, ins.EntityURNs...)
	for _, ref := range knowledgepage.ScanBodyRefs(ins.InsightText) {
		urns = append(urns, ref.URN())
	}
	return urns
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

// pageSnapshot is the page state captured in a changeset before/after image.
type pageSnapshot struct {
	title, summary, body string
	tags                 []string
	version              int
	entityURNs           []string
}

// pageImageWithRefs renders a page before/after snapshot for a changeset value,
// including the page's DataHub reference URNs so a rollback can restore them
// (#664). Phase 0 only writes DataHub references; when the picker and inline
// references land, this snapshot will need to carry the full typed ref set.
func pageImageWithRefs(s pageSnapshot) map[string]any {
	return map[string]any{
		pageFieldTitle:      s.title,
		pageFieldSummary:    s.summary,
		pageFieldBody:       s.body,
		pageFieldTags:       s.tags,
		pageFieldVersion:    s.version,
		pageFieldEntityURNs: s.entityURNs,
	}
}
