package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	// maxPageReferences bounds the explicit references list so a single apply
	// cannot fan out into an unbounded number of per-reference existence queries
	// (#690). Mirrors the JSON-schema maxItems on page.references.
	maxPageReferences = 50
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
	// ValidateRefTargets checks reference targets exist before the page is written,
	// so a bad citation cannot leave a partial page behind (#690).
	ValidateRefTargets(ctx context.Context, refs []knowledgepage.EntityRef) error
	// FilterExistingRefTargets drops references whose target is missing, used for
	// references carried from a source insight that the caller cannot fix (#690).
	FilterExistingRefTargets(ctx context.Context, refs []knowledgepage.EntityRef) ([]knowledgepage.EntityRef, error)
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
	// References are explicit entity references to attach to the page, independent
	// of the body text (#690): a reliable, format-proof citation path for agents.
	// Each is a serialized reference string (mcp:<type>:<id> or urn:li:...), parsed
	// and existence-checked before the page is written, and attached with the
	// promotion (source=promoted) so a rollback undoes it.
	References []string `json:"references,omitempty"`
}

// SetPageWriter wires the knowledge-page store so apply can promote captures to
// canonical pages (the destination is the sink chosen at apply, not the
// capture-time class). A nil store leaves the page sink unavailable (apply with
// sink=knowledge_page then errors rather than silently no-oping).
func (t *Toolkit) SetPageWriter(pw pageWriter) {
	t.pageWriter = pw
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
	originClass, entityURNs, err := t.collectPageInsightRefs(ctx, input.InsightIDs)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Parse the caller's explicit references (independent of body text, #690). A
	// malformed or cross-namespace reference is a clean client error here rather
	// than a silently dropped citation.
	explicitRefs, err := parsePageReferences(page.References)
	if err != nil {
		return errorResult("invalid page reference: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if t.requireConfirmation && !input.Confirm {
		return jsonResult(map[string]any{
			"confirmation_required": true,
			"slug":                  page.Slug,
			fieldMessage:            "Set confirm: true to promote this knowledge to a page.",
		})
	}

	appliedBy := authorFromContext(ctx)

	// Prepare the references that will land on the page (#690), all resolved BEFORE
	// any write: the explicit references[] are hard-validated (a missing target
	// rejects the apply, the caller can fix the payload), while insight-carried and
	// inline body references are filtered to those that still exist (a stale one is
	// skipped, not fatal, so a typo in prose or a deleted entity cannot block the
	// page or leave it partially written).
	plan, err := t.preparePageRefs(ctx, *page, entityURNs, explicitRefs, appliedBy)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	tags := tagsWithOrigin(page.Tags, originClass)
	prom, err := t.applyPagePromotion(ctx, *page, tags, appliedBy, plan)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return t.recordPageChangesetAndMarkApplied(ctx, input, prom, appliedBy)
}

// pageRefPlan is the reference set to write for a promotion (#690). promoted holds
// the explicit references[] merged with the insight-carried references, both written
// first so a target also mentioned in the body is held as a durable promoted
// reference; inline holds the body-scanned references (filtered to those that exist).
type pageRefPlan struct {
	promoted []knowledgepage.EntityRef // explicit references[] + insight-carried refs (written first)
	inline   []knowledgepage.EntityRef // existing references scanned from the body
}

// preparePageRefs builds the reference plan for a promotion before any page write
// (#690). The caller's explicit references[] are existence-checked and a miss is a
// hard error (the caller can fix the payload, and the list is bounded by the
// maxPageReferences cap). Insight-carried references and inline body references are
// filtered to those that still exist, so a stale one (a deleted entity, or a typo in
// prose) is skipped rather than blocking the promotion or FK-failing the inline
// reconcile after the page is written. Explicit and insight references are attached
// as source=promoted and written first, so a target also mentioned in the body is
// stored as a durable promoted reference rather than a body-only inline one.
func (t *Toolkit) preparePageRefs(ctx context.Context, page pagePromotionInput, entityURNs []string, explicit []knowledgepage.EntityRef, appliedBy string) (pageRefPlan, error) {
	if len(explicit) > 0 {
		if err := t.pageWriter.ValidateRefTargets(ctx, explicit); err != nil {
			return pageRefPlan{}, fmt.Errorf("validating page references: %w", err)
		}
	}

	insightRefs, err := t.pageWriter.FilterExistingRefTargets(ctx, promotedRefsFromURNs(entityURNs))
	if err != nil {
		return pageRefPlan{}, fmt.Errorf("filtering insight references: %w", err)
	}

	// Filter the body's inline references to those that exist, so a stale internal
	// (mcp:) token in prose is skipped rather than FK-failing the inline reconcile
	// after the page is written. A DataHub urn:li: ref is free text and always kept.
	inline, err := t.pageWriter.FilterExistingRefTargets(ctx, knowledgepage.ScanBodyRefs(page.Body))
	if err != nil {
		return pageRefPlan{}, fmt.Errorf("filtering inline references: %w", err)
	}

	// Merge explicit + insight references into one promoted set, de-duplicated by
	// target so a target cited both ways is written once.
	promoted := make([]knowledgepage.EntityRef, 0, len(explicit)+len(insightRefs))
	seen := make(map[string]struct{}, len(explicit)+len(insightRefs))
	for _, group := range [][]knowledgepage.EntityRef{explicit, insightRefs} {
		for i := range group {
			if _, dup := seen[group[i].URN()]; dup {
				continue
			}
			seen[group[i].URN()] = struct{}{}
			group[i].CreatedBy = appliedBy
			promoted = append(promoted, group[i])
		}
	}
	return pageRefPlan{promoted: promoted, inline: inline}, nil
}

// pagePromotion holds the result of writing the page, for changeset recording.
type pagePromotion struct {
	pageID     string
	slug       string
	changeType string // changeCreatePage | changeUpdatePage
	prev       map[string]any
	next       map[string]any
}

// applyPagePromotion find-or-creates the page by slug, writes the prepared
// reference plan onto it, and returns the before/after images for the changeset.
//
// Atomicity (#690): every caller-controlled reference is existence-validated by
// preparePageRefs BEFORE this runs, so a bad citation rejects the promotion before
// any write rather than leaving a partial page. The page write, reference writes,
// and changeset record are still separate operations, so a transient store failure
// mid-sequence can leave a written page; the reference writes use ON CONFLICT DO
// NOTHING and re-applying the same slug is idempotent. A single transaction
// spanning page + references + changeset is a broader change tracked separately.
func (t *Toolkit) applyPagePromotion(ctx context.Context, page pagePromotionInput, tags []string, appliedBy string, plan pageRefPlan) (*pagePromotion, error) {
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
		nextURNs, err := t.writePageRefs(ctx, existing.ID, plan)
		if err != nil {
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
		nextURNs, err := t.writePageRefs(ctx, id, plan)
		if err != nil {
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

// writePageRefs writes the prepared reference plan onto a page: the promoted
// references (explicit references[] plus insight-carried refs) are unioned first
// (idempotent ON CONFLICT DO NOTHING), then the page's source=inline references are
// reconciled from the body. Writing promoted first means a target cited both
// explicitly and inline is held as a durable promoted reference; the inline insert
// for that target is then a no-op, not a competing row. It returns the page's
// promoted-reference URNs for the changeset image, so a rollback restores them.
// CreatedBy is already stamped by preparePageRefs.
func (t *Toolkit) writePageRefs(ctx context.Context, pageID string, plan pageRefPlan) ([]string, error) {
	if len(plan.promoted) > 0 {
		if err := t.pageWriter.AddEntityRefs(ctx, pageID, plan.promoted); err != nil {
			return nil, fmt.Errorf("attaching page references: %w", err)
		}
	}
	// Reconcile the body's inline references (#678), identical to the portal save
	// path and rollback. A target already attached as promoted conflicts here and
	// stays promoted.
	if err := t.pageWriter.ReplaceEntityRefsBySource(ctx, pageID, knowledgepage.RefSourceInline, plan.inline); err != nil {
		return nil, fmt.Errorf("reconciling inline page references: %w", err)
	}
	return t.pageEntityURNs(ctx, pageID)
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

// parsePageReferences parses the caller's explicit reference strings (#690) into
// EntityRefs attached by the promotion. They carry source=promoted, the same as
// references carried from a source insight: both are part of what this apply
// attaches, so both are recorded in the changeset and undone by a rollback, and
// neither collides with a human's source=manual picker references. Unlike the body
// scan, which silently skips an unparseable token, an explicit reference that does
// not parse (or crosses the urn:/mcp: namespaces) is a clean error so the caller
// learns the citation was rejected rather than silently dropped.
func parsePageReferences(raw []string) ([]knowledgepage.EntityRef, error) {
	refs := make([]knowledgepage.EntityRef, 0, len(raw))
	for _, s := range raw {
		ref, err := knowledgepage.ParseEntityRef(s)
		if err != nil {
			return nil, fmt.Errorf("reference %q: %w", s, err)
		}
		ref.Source = knowledgepage.RefSourcePromoted
		refs = append(refs, ref)
	}
	return refs, nil
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

// collectPageInsightRefs fetches each source insight and gathers the references
// it carried so they survive promotion onto the page (#664), returning the last
// source insight's class as a non-binding origin tag. Empty insight_ids is
// allowed (a curator authoring a page directly). It does not gate by sink-class:
// the destination is the sink chosen at apply, suggested by whether the insight
// is entity-anchored, not frozen at capture (#686).
func (t *Toolkit) collectPageInsightRefs(ctx context.Context, insightIDs []string) (origin string, entityURNs []string, err error) {
	seen := map[string]struct{}{}
	for _, id := range insightIDs {
		ins, gErr := t.store.Get(ctx, id)
		if gErr != nil {
			return "", nil, fmt.Errorf("insight %s not found", id)
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
	if len(p.References) > maxPageReferences {
		return fmt.Sprintf("page.references exceeds %d entries", maxPageReferences)
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
