package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/agentinstructions"
	"github.com/txn2/mcp-data-platform/internal/toolnames"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// InstructionsTargetURN is the changeset target_urn for a promoted instruction
// section. Exported so a caller listing a target's changesets names the same
// form the sink wrote rather than duplicating the prefix.
func InstructionsTargetURN(section string) string {
	return instructionsTargetPrefix + section
}

// Agent-instructions changeset markers. The changeset target_urn is free-form
// text (migration 000008), so a promotion into the customized instruction layer
// records "ai:<section>" and shares the same changeset store / list / rollback
// surface the DataHub and knowledge-page sinks use.
const (
	instructionsTargetPrefix = "ai:"
	// changeUpdateInstructions records a rule written inline as its own section.
	changeUpdateInstructions = "update_instructions"
	// changeIndexInstructions records a rule too long to stay inline: the body
	// landed on a knowledge page and the section holds a one-line index entry
	// pointing at it. Its changeset carries both images, so one rollback undoes
	// both halves.
	changeIndexInstructions = "index_instructions"

	instructionsFieldText    = "instructions"
	instructionsFieldSection = "section"
	instructionsFieldSlug    = "slug"
	instructionsFieldPageOp  = "page_change_type"
)

// Agent-instructions promotion bounds.
const (
	// maxInstructionsSectionLen bounds the section heading, in runes: it is one
	// heading line, not a title page.
	maxInstructionsSectionLen = 120
	// maxIndexEntryAbout bounds the one line an index entry carries when the
	// caller supplied no summary and it is taken from the rule's opening
	// sentence: the entry is a pointer, not a preview.
	maxIndexEntryAbout = 200
	// maxInlineRuleBytes is where a promoted rule stops being a rule. A body
	// under it stays inline as a section of the customized layer; a longer one
	// lands on a knowledge page and leaves a one-line index entry behind, so the
	// layer stays a short table of contents over a small number of hard rules
	// rather than a document that only grows (#1607).
	maxInlineRuleBytes = 1500
)

// InstructionsStore is the deployment's customized agent-instruction layer as
// the sink and its rollback need it: read the current text, write a new one.
//
// It is the capability rather than the config store, so this toolkit carries
// neither the config-store types nor the config key: the platform owns
// ConfigKeyServerAgentInstructions and adapts its store onto this interface. A
// deployment with no database-backed config store wires nothing, and the sink
// then refuses rather than silently writing to a store that cannot hold the
// value.
type InstructionsStore interface {
	// AgentInstructions returns the deployment's customized instruction text, or
	// "" when the deployment has never set one (which the sink writes into: a
	// first promotion creates the value, the way a first page promotion creates
	// the page).
	AgentInstructions(ctx context.Context) (string, error)
	// SetAgentInstructions replaces the customized instruction text, recording
	// author as the writer.
	SetAgentInstructions(ctx context.Context, value, author string) error
}

// ToolInventory reports every tool name this deployment registers. It is a
// function rather than a slice because the sink is wired before the last toolkit
// registers, so the inventory is read at promotion time.
type ToolInventory func() []string

// InstructionsEditedError is returned when an agent-instructions promotion
// cannot be rolled back because the layer was written again after the
// promotion, so reverting would clobber the later edit. The instructions sink's
// counterpart of PageEditedError.
type InstructionsEditedError struct {
	Section string
}

// Error implements the error interface.
func (e *InstructionsEditedError) Error() string {
	return fmt.Sprintf("rollback blocked: the agent instructions were edited after this changeset "+
		"(section %q); review the current instructions and remove the section manually if needed",
		e.Section)
}

// instructionsPromotionInput is the caller-curated payload on apply
// (sink=agent_instructions), the instruction-layer counterpart of the DataHub
// `changes` list and the page sink's `page` object.
type instructionsPromotionInput struct {
	// Section is the heading the rule lives under, and the find-or-create key: a
	// second promotion of the same section rewrites that section rather than
	// appending a near-duplicate beside it.
	Section string `json:"section,omitempty"`
	// Body is the rule text. Over maxInlineRuleBytes it lands on a knowledge page
	// and the section holds a one-line index entry instead.
	Body string `json:"body,omitempty"`
	// Slug, Title, Summary and Tags describe the knowledge page a diverted body
	// lands on. Slug and Title default to forms derived from the section, and
	// Summary becomes the index entry's one line.
	Slug    string   `json:"slug,omitempty"`
	Title   string   `json:"title,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// References are explicit entity references to attach to a diverted page,
	// exactly as page.references does on sink=knowledge_page.
	References []string `json:"references,omitempty"`
}

// SetInstructionsSink wires the customized agent-instruction layer and the
// deployment's tool inventory, enabling sink=agent_instructions. A nil store
// leaves the sink unavailable (apply with sink=agent_instructions then refuses
// rather than reporting a success nothing recorded); a nil inventory leaves the
// stale-tool-name guard inactive, since there is nothing to measure a reference
// against.
func (t *Toolkit) SetInstructionsSink(store InstructionsStore, tools ToolInventory) {
	t.instructions = store
	t.toolInventory = tools
}

// promoteToInstructions promotes an approved operational_rule capture into the
// deployment's customized agent-instruction layer: found-or-created by section
// heading, recorded as a changeset for audit and rollback parity with the other
// two sinks, and bounded by the layer's byte budget. A body too long to be a
// rule is diverted onto a knowledge page and indexed from the section instead.
func (t *Toolkit) promoteToInstructions(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if t.instructions == nil {
		return toolkit.ErrorResult("agent-instructions promotion is not configured on this deployment " +
			"(it needs a database-backed config store); promote this capture to a knowledge page instead " +
			"with sink=knowledge_page"), nil, nil
	}
	ins := input.Instructions
	if ins == nil {
		return toolkit.ErrorResult("instructions object (section, body) is required for sink=agent_instructions"), nil, nil
	}
	if msg := validateInstructionsPromotion(*ins); msg != "" {
		return toolkit.ErrorResult(msg), nil, nil
	}
	if msg := t.checkInstructionsToolNames(ins.Section + "\n" + ins.Body); msg != "" {
		return toolkit.ErrorResult(msg), nil, nil
	}

	// Mis-routing guard and reference collection, shared with the page sink: the
	// references a source insight carried survive a promotion that diverts onto a
	// page.
	originClass, entityURNs, err := t.collectPageInsightRefs(ctx, input.InsightIDs)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	current, err := t.instructions.AgentInstructions(ctx)
	if err != nil {
		return toolkit.ErrorResult("reading the current agent instructions: " + err.Error()), nil, nil
	}

	if t.requireConfirmation && !input.Confirm {
		return toolkit.JSONResultTyped(map[string]any{
			"confirmation_required":  true,
			instructionsFieldSection: ins.Section,
			fieldMessage: "Set confirm: true to promote this rule into the deployment's agent instructions. " +
				"Every session on this deployment reads them.",
		})
	}

	appliedBy := authorFromContext(ctx)
	if len(ins.Body) > maxInlineRuleBytes {
		return t.promoteInstructionsAsPage(ctx, instructionsWrite{
			input: input, current: current, appliedBy: appliedBy, origin: originClass, entityURNs: entityURNs,
		})
	}
	return t.promoteInstructionsInline(ctx, instructionsWrite{
		input: input, current: current, appliedBy: appliedBy,
	})
}

// instructionsWrite carries the resolved inputs for one agent-instructions
// promotion: the apply request, the layer as it reads now, the author, and the
// origin class and insight-carried references a diverted page needs.
type instructionsWrite struct {
	input      applyKnowledgeInput
	current    string
	appliedBy  string
	origin     string
	entityURNs []string
}

// promoteInstructionsInline writes the rule as its own section of the
// customized layer, leaving every other section byte-identical.
func (t *Toolkit) promoteInstructionsInline(ctx context.Context, w instructionsWrite) (*mcp.CallToolResult, any, error) {
	ins := w.input.Instructions
	next, created, err := upsertInstructionSection(w.current, ins.Section, strings.TrimSpace(ins.Body))
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if err := agentinstructions.CheckCustomizedSize(next); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if err := t.instructions.SetAgentInstructions(ctx, next, w.appliedBy); err != nil {
		return toolkit.ErrorResult("writing the agent instructions: " + err.Error()), nil, nil
	}

	return t.recordInstructionsChangeset(ctx, w, instructionsPromotion{
		section:    ins.Section,
		changeType: changeUpdateInstructions,
		created:    created,
		prev:       map[string]any{instructionsFieldText: w.current},
		next:       map[string]any{instructionsFieldText: next, instructionsFieldSection: ins.Section},
		text:       next,
	})
}

// promoteInstructionsAsPage handles a body too long to be a rule: it lands on a
// knowledge page (find-or-created by slug, exactly as sink=knowledge_page does)
// and the section holds one index entry pointing at it. Both halves are one
// changeset, so a single rollback restores the layer and reverts the page.
func (t *Toolkit) promoteInstructionsAsPage(ctx context.Context, w instructionsWrite) (*mcp.CallToolResult, any, error) {
	ins := w.input.Instructions
	if t.pageWriter == nil {
		return toolkit.ErrorResult(fmt.Sprintf(
			"the rule body is %d bytes, over the %d-byte inline limit, so it belongs on a knowledge page -- "+
				"but knowledge-page promotion is not configured on this deployment; shorten the rule to a "+
				"single operating instruction, or configure the knowledge-page sink",
			len(ins.Body), maxInlineRuleBytes)), nil, nil
	}

	page := instructionsPageInput(*ins)
	if page.Slug == "" {
		return toolkit.ErrorResult(fmt.Sprintf(
			"the rule body is %d bytes, over the %d-byte inline limit, so it belongs on a knowledge page, "+
				"but instructions.section %q yields no page slug; pass instructions.slug",
			len(ins.Body), maxInlineRuleBytes, ins.Section)), nil, nil
	}
	if msg := validatePagePromotion(page); msg != "" {
		return toolkit.ErrorResult(msg), nil, nil
	}

	// The section's new text is one index entry, so the layer's size is settled
	// before anything is written: an over-budget promotion is refused before it
	// leaves a page behind.
	entry := agentinstructions.IndexEntry(page.Slug, indexEntryAbout(*ins))
	next, created, err := upsertInstructionSection(w.current, ins.Section, entry)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if err := agentinstructions.CheckCustomizedSize(next); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	prom, err := t.writeInstructionsPage(ctx, page, w)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if err := t.instructions.SetAgentInstructions(ctx, next, w.appliedBy); err != nil {
		return toolkit.ErrorResult("wrote the knowledge page but writing the agent instructions failed: " + err.Error()), nil, nil
	}

	// The page and instruction images share one flat value map: their keys are
	// disjoint, so one changeset carries both and applyPageRevert reads the page
	// half unchanged.
	prev := map[string]any{instructionsFieldText: w.current}
	nextVal := map[string]any{
		instructionsFieldText:    next,
		instructionsFieldSection: ins.Section,
		instructionsFieldSlug:    page.Slug,
		instructionsFieldPageOp:  prom.changeType,
	}
	maps.Copy(prev, prom.prev)
	maps.Copy(nextVal, prom.next)
	return t.recordInstructionsChangeset(ctx, w, instructionsPromotion{
		section: ins.Section, changeType: changeIndexInstructions, created: created,
		prev: prev, next: nextVal, text: next, slug: page.Slug, pageID: prom.pageID,
	})
}

// writeInstructionsPage writes the diverted body onto a knowledge page through
// the page sink's own write path, so a page created this way is
// indistinguishable from one promoted with sink=knowledge_page: found-or-created
// by slug, references validated before the write, built-in slugs refused.
func (t *Toolkit) writeInstructionsPage(ctx context.Context, page pagePromotionInput, w instructionsWrite) (*pagePromotion, error) {
	existing, err := t.lookupExistingPage(ctx, page.Slug)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Builtin {
		return nil, fmt.Errorf("slug %q is a built-in platform documentation page and is read-only; "+
			"promote under a different slug by naming instructions.slug", page.Slug)
	}
	explicit, err := parsePageReferences(page.References)
	if err != nil {
		return nil, fmt.Errorf("invalid page reference: %w", err)
	}
	plan, err := t.preparePageRefs(ctx, page, w.entityURNs, explicit, w.appliedBy)
	if err != nil {
		return nil, err
	}
	return t.applyPagePromotion(ctx, pageWrite{
		input:     page,
		tags:      tagsWithOrigin(page.Tags, w.origin),
		appliedBy: w.appliedBy,
		plan:      plan,
		existing:  existing,
	})
}

// instructionsPromotion holds the result of writing one promotion, for
// changeset recording and the response.
type instructionsPromotion struct {
	section    string
	changeType string // changeUpdateInstructions | changeIndexInstructions
	created    bool   // the section did not exist before this promotion
	prev, next map[string]any
	text       string // the customized layer as this promotion left it
	slug       string // the diverted page's slug, empty for an inline rule
	pageID     string
}

// recordInstructionsChangeset records the promotion changeset (target
// "ai:<section>") and marks the source insights applied, mirroring
// recordPageChangesetAndMarkApplied for the page path.
func (t *Toolkit) recordInstructionsChangeset(ctx context.Context, w instructionsWrite, prom instructionsPromotion) (*mcp.CallToolResult, any, error) {
	csID, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("internal error generating changeset ID"), nil, nil
	}
	insightIDs := w.input.InsightIDs
	if insightIDs == nil {
		insightIDs = []string{}
	}
	if err := t.changesetStore.InsertChangeset(ctx, Changeset{
		ID:               csID,
		TargetURN:        InstructionsTargetURN(prom.section),
		ChangeType:       prom.changeType,
		PreviousValue:    prom.prev,
		NewValue:         prom.next,
		SourceInsightIDs: insightIDs,
		AppliedBy:        w.appliedBy,
	}); err != nil {
		return toolkit.ErrorResult("failed to record changeset: " + err.Error()), nil, nil
	}
	for _, insID := range insightIDs {
		if err := t.store.MarkApplied(ctx, insID, w.appliedBy, csID); err != nil {
			slog.Warn("knowledge: failed to mark insight applied",
				"insight_id", insID, "changeset_id", csID, "error", err)
		}
	}
	return toolkit.JSONResultTyped(instructionsResult(csID, prom, len(insightIDs)))
}

// instructionsResult builds the apply response for a promotion into the
// customized layer.
func instructionsResult(csID string, prom instructionsPromotion, insights int) map[string]any {
	action := "updated"
	if prom.created {
		action = "created"
	}
	msg := fmt.Sprintf("Agent-instruction section %q %s. Roll back with action=rollback changeset_id=%s.",
		prom.section, action, csID)
	result := map[string]any{
		"changeset_id":           csID,
		instructionsFieldSection: prom.section,
		// target_urn is what list_changesets takes, so the caller can find this
		// promotion again without knowing how the sink keys its changesets.
		"target_urn":              InstructionsTargetURN(prom.section),
		"action":                  action,
		"insights_marked_applied": insights,
		"instructions_bytes":      len(prom.text),
		"instructions_limit":      agentinstructions.MaxCustomizedBytes,
		// An instructions promotion reverts through this sink, so it is always
		// structurally revertible; the field is the same one every apply response
		// carries (#922).
		"revertible": true,
	}
	if prom.slug != "" {
		result[instructionsFieldSlug] = prom.slug
		result["page_id"] = prom.pageID
		msg = fmt.Sprintf("The rule was longer than the %d-byte inline limit, so it was written to knowledge page %q "+
			"and agent-instruction section %q %s holding one index entry pointing at it. "+
			"Roll back with action=rollback changeset_id=%s.",
			maxInlineRuleBytes, prom.slug, prom.section, action, csID)
	}
	if notice := agentinstructions.CustomizedNotice(prom.text); notice != "" {
		result["size_notice"] = notice
		msg += " " + notice
	}
	result[fieldMessage] = msg
	return result
}

// upsertInstructionSection writes body under the section heading, replacing the
// section when it exists and appending it when it does not. The replacement runs
// through the anchored-edit engine (pkg/textpatch), which spans a heading through
// the next heading of the same or higher level, so every other section of the
// document is left byte-identical.
//
// A section name matching more than one heading is an error rather than a guess:
// overwriting one of two same-named sections, or appending a third, both lose
// content the caller did not ask to lose.
func upsertInstructionSection(doc, section, body string) (next string, created bool, err error) {
	sec, findErr := textpatch.FindSection(doc, section, -1)
	switch {
	case findErr == nil:
		if nested := nestedHeadings(doc, section); len(nested) > 0 {
			return "", false, fmt.Errorf("agent-instruction section %q owns %d nested heading(s) (%s), "+
				"which a rewrite of it would replace along with it; promote onto one of them instead",
				section, len(nested), strings.Join(nested, ", "))
		}
		// The heading is reused exactly as the document wrote it, so a promotion
		// onto a section an operator wrote at another level does not silently
		// change its level.
		res, aErr := textpatch.Apply(doc, []textpatch.Edit{{
			Op:      textpatch.OpReplaceSection,
			Section: section,
			Text:    sec.Heading + "\n\n" + body + "\n\n",
		}}, textpatch.Options{})
		if aErr != nil {
			return "", false, fmt.Errorf("rewriting agent-instruction section %q: %w", section, aErr)
		}
		return res.Body, false, nil
	case isSectionNotFound(findErr):
		text := renderInstructionSection(section, body)
		if strings.TrimSpace(doc) == "" {
			return text, true, nil
		}
		return strings.TrimRight(doc, "\n") + "\n\n" + text, true, nil
	default:
		return "", false, fmt.Errorf("resolving agent-instruction section %q: %w", section, findErr)
	}
}

// nestedHeadings returns the headings a section owns beneath its own. A section
// spans its heading through the next heading of the same or higher level, so a
// rewrite replaces everything under it: an operator's "# Deployment notes" with
// four "##" sections beneath it would lose all four to one promotion. Naming
// them and refusing is the alternative to deleting them silently.
func nestedHeadings(doc, section string) []string {
	text, _, err := textpatch.SectionText(doc, section)
	if err != nil {
		return nil
	}
	var nested []string
	for _, s := range textpatch.Outline(text, textpatch.SyntaxMarkdown) {
		if s.Line == 1 {
			// The section's own heading opens its text.
			continue
		}
		nested = append(nested, s.Heading)
	}
	return nested
}

// isSectionNotFound reports whether a textpatch failure is "no such section",
// the one outcome that means "create it" rather than "stop".
func isSectionNotFound(err error) bool {
	var pErr *textpatch.Error
	return errors.As(err, &pErr) && pErr.Code == textpatch.CodeSectionNotFound
}

// renderInstructionSection renders one section of the customized layer: a level-2
// heading, the body, and a trailing blank line so the next heading is not glued
// to it when this section is rewritten in place.
func renderInstructionSection(section, body string) string {
	return "## " + section + "\n\n" + strings.TrimSpace(body) + "\n\n"
}

// instructionsPageInput builds the knowledge page a diverted rule lands on,
// defaulting the slug and title from the section heading so a caller promoting a
// rule need not decide it is about to become a page.
func instructionsPageInput(ins instructionsPromotionInput) pagePromotionInput {
	slug := strings.TrimSpace(ins.Slug)
	if slug == "" {
		slug = sectionSlug(ins.Section)
	}
	title := strings.TrimSpace(ins.Title)
	if title == "" {
		title = ins.Section
	}
	return pagePromotionInput{
		Slug:       slug,
		Title:      title,
		Summary:    ins.Summary,
		Body:       ins.Body,
		Tags:       ins.Tags,
		References: ins.References,
	}
}

// indexEntryAbout is the one line the index entry carries: the caller's summary
// when there is one, else the rule's opening sentence, so the pointer says what
// reading the page answers rather than only where it is.
func indexEntryAbout(ins instructionsPromotionInput) string {
	if s := strings.TrimSpace(ins.Summary); s != "" {
		return s
	}
	body := strings.TrimSpace(ins.Body)
	if i := strings.IndexAny(body, ".\n"); i > 0 {
		body = body[:i]
	}
	if len(body) > maxIndexEntryAbout {
		body = strings.TrimSpace(body[:maxIndexEntryAbout])
	}
	return body
}

// validateInstructionsPromotion checks the caller-supplied instructions payload.
func validateInstructionsPromotion(ins instructionsPromotionInput) string {
	if strings.TrimSpace(ins.Section) == "" {
		return "instructions.section is required for sink=agent_instructions"
	}
	if strings.TrimSpace(ins.Body) == "" {
		return "instructions.body is required for sink=agent_instructions"
	}
	if strings.ContainsAny(ins.Section, "\n\r#") {
		return "instructions.section is one heading line: it cannot contain a newline or a '#'"
	}
	if utf8.RuneCountInString(ins.Section) > maxInstructionsSectionLen {
		return fmt.Sprintf("instructions.section exceeds %d characters", maxInstructionsSectionLen)
	}
	if len(ins.Body) > maxPageBodyLen {
		return fmt.Sprintf("instructions.body exceeds %d bytes", maxPageBodyLen)
	}
	// The summary becomes the index entry a diverted rule leaves behind, and the
	// page's summary field, so it obeys the page store's own bound under the name
	// the caller passed it by.
	if utf8.RuneCountInString(ins.Summary) > maxPageSummaryLen {
		return fmt.Sprintf("instructions.summary exceeds %d characters", maxPageSummaryLen)
	}
	return ""
}

// checkInstructionsToolNames refuses a promotion naming a tool this deployment
// does not register, so a rule cannot instruct every future session to call
// something that is not there. The token set is derived from the deployment's own
// inventory (toolnames.Unknown), which is why it catches a stale
// api_, manage_, memory_ or save_ name and not only the six prefixes the
// startup-only lint used to know about (#1607, #1608).
//
// It returns "" when the guard has no inventory to measure against, since
// refusing every snake_case token would be worse than refusing none.
func (t *Toolkit) checkInstructionsToolNames(text string) string {
	if t.toolInventory == nil {
		return ""
	}
	unknown := toolnames.Unknown(text, t.toolInventory())
	if len(unknown) == 0 {
		return ""
	}
	return fmt.Sprintf("this promotion names %d tool(s) this deployment does not register: %s. "+
		"A rule every session reads cannot instruct one to call a tool that is not there: correct the "+
		"name, or remove the reference if the tool was retired.",
		len(unknown), strings.Join(unknown, ", "))
}

// sectionSlug derives a knowledge-page slug from a section heading, so a
// promotion diverted onto a page needs no slug from the caller: every run of
// characters a slug cannot carry collapses to one hyphen.
func sectionSlug(section string) string {
	lowered := strings.ToLower(strings.TrimSpace(section))
	out := make([]byte, 0, len(lowered))
	hyphen := false
	// Byte-wise, not rune-wise: every byte of a multi-byte rune falls in the
	// separator branch, which the hyphen latch collapses to one hyphen, so the
	// result is the same without narrowing a rune to a byte.
	for i := 0; i < len(lowered); i++ {
		c := lowered[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
			hyphen = false
		case !hyphen && len(out) > 0:
			out = append(out, '-')
			hyphen = true
		}
	}
	slug := strings.Trim(string(out), "-")
	if len(slug) > maxPageSlugLen {
		slug = strings.Trim(slug[:maxPageSlugLen], "-")
	}
	return slug
}

// revertInstructionsChangeset reverts a promotion into the customized
// agent-instruction layer: the layer is restored to the text the changeset
// recorded as its before-image, and a promotion that diverted its body onto a
// knowledge page also reverts that page (a created page is soft-deleted, an
// updated one restored). Both halves are recorded in the one changeset, so one
// rollback undoes both.
//
// It refuses (InstructionsEditedError) when the layer no longer holds the text
// this promotion produced, so a rollback never clobbers a later edit -- the
// instruction-layer counterpart of the page sink's version check. Pure: it
// returns a RollbackResult and typed errors so the apply_knowledge tool and the
// admin REST endpoint present failures uniformly.
func revertInstructionsChangeset(ctx context.Context, deps RollbackDeps, cs *Changeset, rolledBackBy string) (*RollbackResult, error) {
	if deps.Instructions == nil {
		return nil, errors.New("agent-instructions rollback is not configured on this deployment")
	}
	section := strings.TrimPrefix(cs.TargetURN, instructionsTargetPrefix)
	current, err := deps.Instructions.AgentInstructions(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the current agent instructions: %w", err)
	}
	if current != stringField(cs.NewValue, instructionsFieldText) {
		return nil, &InstructionsEditedError{Section: section}
	}

	// A diverted promotion's page is resolved before anything is written, so a
	// page that cannot be reverted refuses the whole rollback with nothing
	// changed.
	var page *knowledgepage.Page
	if cs.ChangeType == changeIndexInstructions {
		page, err = resolveIndexedPage(ctx, deps, cs)
		if err != nil {
			return nil, err
		}
	}

	// The layer is restored first, so the two writes it takes fail in the
	// harmless direction: the restored text no longer references the page, so a
	// failure between them leaves a page nothing points at rather than
	// instructions pointing at a page that is gone.
	prior := stringField(cs.PreviousValue, instructionsFieldText)
	if err := deps.Instructions.SetAgentInstructions(ctx, prior, rolledBackBy); err != nil {
		return nil, fmt.Errorf("restoring the agent instructions: %w", err)
	}
	reverted := []string{"restored agent-instruction section " + section}

	if page != nil {
		pageReverted, pErr := applyPageRevert(ctx, pageRevert{
			pages: deps.Pages, cs: cs, op: stringField(cs.NewValue, instructionsFieldPageOp),
			page: page, by: rolledBackBy,
		})
		if pErr != nil {
			return nil, fmt.Errorf("restored the agent instructions, but knowledge page %q was left in place "+
				"and can be removed in the portal: %w", page.Slug, pErr)
		}
		reverted = append(reverted, pageReverted)
	}

	returnedInsights := returnInsightsToReview(ctx, deps.Insights, cs.SourceInsightIDs, rolledBackBy, cs.ID)
	if err := deps.Changesets.RollbackChangeset(ctx, cs.ID, rolledBackBy); err != nil {
		return nil, fmt.Errorf("restored the agent instructions but recording the rollback failed: %w", err)
	}
	return &RollbackResult{
		ChangesetID:              cs.ID,
		TargetURN:                cs.TargetURN,
		RevertedChanges:          reverted,
		InsightsReturnedToReview: returnedInsights,
		RolledBackBy:             rolledBackBy,
	}, nil
}

// resolveIndexedPage looks up the knowledge page a diverted rule landed on and
// checks it is still the version the promotion produced, so a page that cannot
// be reverted refuses the rollback before the layer is touched. It does not
// write: the inverse operation runs afterwards through applyPageRevert.
func resolveIndexedPage(ctx context.Context, deps RollbackDeps, cs *Changeset) (*knowledgepage.Page, error) {
	if deps.Pages == nil {
		return nil, errors.New("this changeset also wrote a knowledge page, and knowledge-page rollback " +
			"is not configured on this deployment")
	}
	slug := stringField(cs.NewValue, instructionsFieldSlug)
	page, err := deps.Pages.GetBySlug(ctx, slug)
	if errors.Is(err, knowledgepage.ErrNotFound) {
		return nil, fmt.Errorf("knowledge page no longer exists: %s", slug)
	}
	if err != nil {
		return nil, fmt.Errorf("looking up knowledge page: %w", err)
	}
	if produced := intFromMap(cs.NewValue, pageFieldVersion); page.CurrentVersion != produced {
		return nil, &PageEditedError{Slug: slug, CurrentVersion: page.CurrentVersion, ChangesetVersion: produced}
	}
	return page, nil
}
