package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// memoryCaptureToolName is the unified write verb (#633). It lives in the memory
// toolkit (not knowledge) so creating memory never requires the knowledge
// toolkit to be enabled.
const memoryCaptureToolName = "memory_capture"

// recallSupersedeThreshold is the minimum cosine similarity at which a new
// capture is treated as a restatement of an existing record (and supersedes it
// instead of appending a duplicate). It is compared against the raw cosine
// returned by VectorSearch, so 0.9 means "near-identical text". Tunable.
const recallSupersedeThreshold = 0.9

// recallSuggestThreshold is the minimum cosine similarity at which an existing
// record is surfaced as a similar_existing candidate in the capture response
// (#762): close enough that the agent should decide update-vs-create, but not
// close enough to auto-supersede. Matches in [suggest, supersede) are returned;
// matches at or above recallSupersedeThreshold are superseded automatically.
const recallSuggestThreshold = 0.75

// maxSuggestedActions caps the catalog-change proposals a single capture may
// carry, mirroring knowledge.MaxSuggestedActions.
const maxSuggestedActions = 5

// logKeyError is the slog attribute key for errors in this file.
const logKeyError = "error"

// RecallQuery is the recall-first lookup: the precomputed embedding of the
// candidate content, the entities it concerns, and the caller's email, plus the
// cosine threshold above which a prior record counts as similar. Embedding
// is empty when no embedder is configured; in that case recall is skipped (no
// reliable similarity, so the capture simply appends).
type RecallQuery struct {
	Embedding   []float32
	EntityURNs  []string
	CallerEmail string
	MinScore    float64
}

// RecallMatch is one existing record similar to a new capture, with its raw
// cosine score. The capture path splits matches by score: at or above
// recallSupersedeThreshold the record is superseded; below it the match is
// returned to the agent as a similar_existing candidate.
type RecallMatch struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// RecallChecker finds the caller's active records a new capture restates, so
// the write path can supersede instead of appending (recall-first, #633) and
// surface near-matches for the agent to consolidate (#762). Implemented by
// the platform over the memory store; declared here so this package does not
// import pkg/knowledge.
type RecallChecker interface {
	// Matches returns the caller's active records with cosine similarity at or
	// above q.MinScore, best first. When the candidate carries entity URNs,
	// matches must share at least one (knowledge about table A never matches
	// knowledge about table B).
	Matches(ctx context.Context, q RecallQuery) ([]RecallMatch, error)
}

// ThreadLinker bridges a reviewed capture back to the feedback thread(s) it
// resolves (#602). Satisfied by the portal thread store; a minimal interface so
// the memory toolkit does not depend on the portal package.
type ThreadLinker interface {
	LinkInsight(ctx context.Context, threadIDs []string, insightID, actorID, actorEmail string) ([]string, error)
}

// SetRecallChecker wires the recall-first checker.
func (t *Toolkit) SetRecallChecker(rc RecallChecker) { t.recallChecker = rc }

// SetThreadLinker wires the feedback-thread bridge.
func (t *Toolkit) SetThreadLinker(tl ThreadLinker) { t.threadLinker = tl }

// suggestedActionInput mirrors the catalog-change proposal shape so it round-
// trips through metadata to apply_knowledge (whose SuggestedAction uses the same
// JSON tags). Kept local so the memory toolkit does not import the knowledge
// package.
type suggestedActionInput struct {
	ActionType       string `json:"action_type"`
	Target           string `json:"target"`
	Detail           string `json:"detail"`
	QuerySQL         string `json:"query_sql,omitempty"`
	QueryDescription string `json:"query_description,omitempty"`
}

// validCaptureActionTypes is the set of accepted suggested-action types. It
// duplicates knowledge.validActionTypes because pkg/knowledge imports this
// package's sibling (memory_adapter), so importing knowledge here would create
// an import cycle. Keep in sync with knowledge/types.go.
var validCaptureActionTypes = map[string]bool{
	"update_description": true, "add_tag": true, "remove_tag": true,
	"add_glossary_term": true, "flag_quality_issue": true, "add_documentation": true,
	"add_curated_query": true, "set_structured_property": true, "remove_structured_property": true,
	"raise_incident": true, "resolve_incident": true,
	"add_context_document": true, "update_context_document": true, "remove_context_document": true,
	"add_prompt": true,
}

// memoryCaptureInput is the deserialized memory_capture input. type (sink-class)
// is the organizing axis; the rest are optional attachments.
type memoryCaptureInput struct {
	Type             string                   `json:"type"`
	Content          string                   `json:"content"`
	Category         string                   `json:"category,omitempty"`
	EntityURNs       []string                 `json:"entity_urns,omitempty"`
	RelatedColumns   []memstore.RelatedColumn `json:"related_columns,omitempty"`
	SuggestedActions []suggestedActionInput   `json:"suggested_actions,omitempty"`
	Confidence       string                   `json:"confidence,omitempty"`
	Source           string                   `json:"source,omitempty"`
	ThreadIDs        []string                 `json:"thread_ids,omitempty"`
	Metadata         map[string]any           `json:"metadata,omitempty"`
}

// memoryCaptureOutput is the memory_capture success response.
type memoryCaptureOutput struct {
	ID        string `json:"id"`
	SinkClass string `json:"sink_class"`
	Status    string `json:"status"`
	// Superseded keeps the original wire shape (a single id, the best match)
	// so existing consumers of the field keep parsing; SupersededIDs carries
	// the complete list now that one capture can consolidate several
	// restatements (#762).
	Superseded    string   `json:"superseded,omitempty"`
	SupersededIDs []string `json:"superseded_ids,omitempty"`
	// SimilarExisting lists active records similar to this capture but below
	// the auto-supersede threshold, so the agent can decide whether the new
	// capture restates one of them and consolidate (memory_manage update /
	// consolidate) instead of leaving a near-duplicate behind (#762).
	SimilarExisting   []RecallMatch `json:"similar_existing,omitempty"`
	Message           string        `json:"message"`
	LinkedThreadCount int           `json:"linked_thread_count,omitempty"`
	UnlinkedThreadIDs []string      `json:"unlinked_thread_ids,omitempty"`
}

// handleMemoryCapture is the unified write verb. It validates the input, finds
// any prior record this capture restates (recall-first, BEFORE the insert so the
// new row cannot match itself), inserts, then supersedes the prior record. It
// routes by sink-class: live classes (personal_preference, episodic_event) are
// active immediately; reviewed classes carry the pending insight overlay so
// apply_knowledge can later promote them.
func (t *Toolkit) handleMemoryCapture(ctx context.Context, _ *mcp.CallToolRequest, input memoryCaptureInput) (*mcp.CallToolResult, any, error) {
	content := strings.TrimSpace(input.Content)
	if msg := validateCaptureInput(input, content); msg != "" {
		return toolkit.ErrorResult(msg), nil, nil
	}

	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserEmail == "" {
		return toolkit.ErrorResult("a user identity (email) is required to capture knowledge"), nil, nil
	}

	id, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("failed to generate ID"), nil, nil
	}

	actor := captureActor{UserID: pc.UserID, Email: pc.UserEmail, Persona: pc.PersonaName, SessionID: pc.SessionID}
	rec := t.buildCaptureRecord(id, content, input, actor)

	out, err := t.applyCapture(ctx, &rec, input.Type, actor, input.ThreadIDs)
	if err != nil {
		return toolkit.ErrorResult("failed to capture: " + err.Error()), nil, nil
	}

	return captureSuccess(rec, out)
}

// captureOutcome carries the side results of the shared write pipeline.
type captureOutcome struct {
	Superseded []string
	Similar    []RecallMatch
	Linked     int
	Unlinked   []string
}

// captureActor carries the identity a capture is attributed to. The
// memory_capture tool fills it from the request's PlatformContext; server-
// initiated captures (AutoCapture) supply it explicitly, since a platform-
// minted record has no incoming request context.
type captureActor struct {
	UserID    string
	Email     string
	Persona   string
	SessionID string
}

// applyCapture runs the shared write pipeline for an already-assembled record:
// embed, recall-first supersede check (BEFORE the insert so the new row cannot
// match itself), insert, supersede, then thread-link. Both the memory_capture
// tool and AutoCapture funnel through here so server-initiated captures get
// identical semantics. The record is mutated in place to carry its embedding.
func (t *Toolkit) applyCapture(ctx context.Context, rec *memstore.Record, sinkClass string, actor captureActor, threadIDs []string) (captureOutcome, error) {
	t.embedCaptureRecord(ctx, rec, rec.Content)

	// Recall-first reuses the embedding just computed (no second embed call).
	restated, similar := t.findPriorMatches(ctx, *rec)

	if err := t.store.Insert(ctx, *rec); err != nil {
		return captureOutcome{}, fmt.Errorf("insert capture: %w", err)
	}

	linked, unlinked := t.linkCaptureThreads(ctx, actor, rec.ID, sinkClass, threadIDs)
	return captureOutcome{
		Superseded: t.applySupersedes(ctx, restated, rec.ID),
		Similar:    similar,
		Linked:     linked,
		Unlinked:   unlinked,
	}, nil
}

// validateCaptureInput returns the first validation failure message, or "" when
// the input is valid. It enforces the same invariants the retired capture_insight
// and memory_manage(remember) tools did, so nothing unvalidated reaches the store
// or, later, apply_knowledge.
func validateCaptureInput(input memoryCaptureInput, content string) string {
	for _, err := range []error{
		memstore.ValidateSinkClass(input.Type),
		memstore.ValidateContent(content),
		memstore.ValidateEntityURNs(input.EntityURNs),
		memstore.ValidateRelatedColumns(input.RelatedColumns),
		memstore.ValidateCategory(input.Category),
		memstore.ValidateConfidence(input.Confidence),
		memstore.ValidateSource(input.Source),
		validateSuggestedActions(input.SuggestedActions),
	} {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

// validateSuggestedActions enforces the same limits as the knowledge apply path
// (max count, known action_type, query_sql required for add_curated_query) so a
// capture can never persist a proposal apply_knowledge would later reject.
func validateSuggestedActions(actions []suggestedActionInput) error {
	if len(actions) > maxSuggestedActions {
		return fmt.Errorf("suggested_actions exceeds maximum of %d (got %d)", maxSuggestedActions, len(actions))
	}
	for i, a := range actions {
		if !validCaptureActionTypes[a.ActionType] {
			return fmt.Errorf("suggested_actions[%d]: invalid action_type %q", i, a.ActionType)
		}
		if a.ActionType == "add_curated_query" && a.QuerySQL == "" {
			return fmt.Errorf("suggested_actions[%d]: query_sql is required for add_curated_query", i)
		}
	}
	return nil
}

// buildCaptureRecord assembles the memory record for a capture, applying the
// sink-class routing: dimension, live-vs-reviewed status overlay, and metadata.
func (*Toolkit) buildCaptureRecord(id, content string, input memoryCaptureInput, actor captureActor) memstore.Record {
	return memstore.Record{
		ID:             id,
		CreatedBy:      actor.Email,
		Persona:        actor.Persona,
		Dimension:      memstore.SinkClassDimension(input.Type),
		SinkClass:      input.Type,
		Content:        content,
		Category:       memstore.NormalizeCategory(input.Category),
		Confidence:     memstore.NormalizeConfidence(input.Confidence),
		Source:         memstore.NormalizeSource(input.Source),
		EntityURNs:     input.EntityURNs,
		RelatedColumns: input.RelatedColumns,
		Status:         memstore.StatusActive,
		Metadata:       captureMetadata(input.Type, actor.SessionID, input.SuggestedActions, input.Metadata),
	}
}

// captureMetadata builds the record metadata, adding the pending insight overlay
// (review state + catalog proposals + session) for reviewed sink-classes so
// apply_knowledge surfaces them as pending insights. Identity-agnostic (takes a
// sessionID, not a PlatformContext) so both the tool and AutoCapture share it.
func captureMetadata(sinkClass, sessionID string, suggestedActions []suggestedActionInput, extra map[string]any) map[string]any {
	meta := map[string]any{}
	maps.Copy(meta, extra)
	if !memstore.SinkClassIsLive(sinkClass) {
		meta[memstore.MetaKeyInsightStatus] = memstore.InsightStatusPending
		if sessionID != "" {
			meta[memstore.MetaKeySessionID] = sessionID
		}
		if len(suggestedActions) > 0 {
			meta[memstore.MetaKeySuggestedActions] = suggestedActions
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// embedCaptureRecord stamps an embedding when a real embedder is configured
// (best-effort; an embed failure leaves the row lexical-only and disables
// recall-first dedup for this capture).
func (t *Toolkit) embedCaptureRecord(ctx context.Context, rec *memstore.Record, content string) {
	if !embedding.IsConfigured(t.embedder) {
		return
	}
	emb, err := t.embedder.Embed(ctx, content)
	if err != nil {
		slog.Warn("memory_capture: embedding failed, storing without", logKeyError, err)
		return
	}
	rec.Embedding = emb
	rec.EmbeddingModel, rec.EmbeddingTextHash = t.embeddingBreadcrumbs(emb, content)
}

// findPriorMatches returns the existing records this capture restates
// (similarity at or above the supersede threshold, all of them — so a capture
// arriving over an already-duplicated pair consolidates the whole set; the
// blast radius is bounded by the recall candidate limit and by the threshold,
// which at 0.9 raw cosine means near-identical text, and every superseded id
// is reported in the response) and the records similar enough to surface as
// candidates but not to auto-supersede.
// Both are empty when recall is unavailable (no checker, no embedding) or
// nothing clears the suggest threshold. Best-effort: a recall error never
// fails the capture.
func (t *Toolkit) findPriorMatches(ctx context.Context, rec memstore.Record) (restated, similar []RecallMatch) {
	if t.recallChecker == nil || len(rec.Embedding) == 0 {
		return nil, nil
	}
	matches, err := t.recallChecker.Matches(ctx, RecallQuery{
		Embedding:   rec.Embedding,
		EntityURNs:  rec.EntityURNs,
		CallerEmail: rec.CreatedBy,
		MinScore:    recallSuggestThreshold,
	})
	if err != nil {
		slog.Debug("memory_capture: recall-first check failed", logKeyError, err)
		return nil, nil
	}
	for _, m := range matches {
		if m.Score >= recallSupersedeThreshold {
			restated = append(restated, m)
		} else {
			similar = append(similar, m)
		}
	}
	return restated, similar
}

// applySupersedes marks every restated record superseded by the new capture.
// Best-effort per record: a failure is logged and the capture still succeeds
// (the new row is already stored); only records actually superseded are
// returned so the caller never falsely claims a supersede.
func (t *Toolkit) applySupersedes(ctx context.Context, restated []RecallMatch, newID string) []string {
	var superseded []string
	for _, m := range restated {
		if m.ID == "" || m.ID == newID {
			continue
		}
		if err := t.store.Supersede(ctx, m.ID, newID); err != nil {
			slog.Warn("memory_capture: failed to supersede prior record", "old", m.ID, "new", newID, logKeyError, err)
			continue
		}
		superseded = append(superseded, m.ID)
	}
	return superseded
}

// linkCaptureThreads bridges a reviewed capture to feedback threads (#602).
// Thread linking is a review-loop concept, so live captures (and captures with
// no linker wired) surface the thread_ids as unlinked rather than silently
// dropping them.
func (t *Toolkit) linkCaptureThreads(ctx context.Context, actor captureActor, id, sinkClass string, threadIDs []string) (linked int, unlinked []string) {
	if len(threadIDs) == 0 {
		return 0, nil
	}
	if memstore.SinkClassIsLive(sinkClass) || t.threadLinker == nil {
		return 0, threadIDs
	}
	linkedIDs, err := t.threadLinker.LinkInsight(ctx, threadIDs, id, actor.UserID, actor.Email)
	if err != nil {
		slog.Warn("memory_capture: failed to link threads", "id", id, logKeyError, err)
		return 0, threadIDs
	}
	return len(linkedIDs), missingFrom(threadIDs, linkedIDs)
}

// captureSuccess marshals the success response.
func captureSuccess(rec memstore.Record, out captureOutcome) (*mcp.CallToolResult, any, error) {
	msg := "Captured. "
	if memstore.SinkClassIsLive(rec.SinkClass) {
		msg += "Available to you immediately."
	} else {
		msg += "It will be reviewed before promotion to a shared catalog."
	}
	if n := len(out.Superseded); n > 0 {
		msg += fmt.Sprintf(" %d prior record(s) superseded.", n)
	}
	if len(out.Similar) > 0 {
		msg += " Similar existing records found (similar_existing): if this restates one," +
			" consolidate with memory_manage (update the existing record, or consolidate the duplicate)."
	}
	// Matches arrive best-first, so the first superseded id is the best match
	// (the singular field's original meaning).
	var best string
	if len(out.Superseded) > 0 {
		best = out.Superseded[0]
	}
	return toolkit.JSONResult(memoryCaptureOutput{
		ID:                rec.ID,
		SinkClass:         rec.SinkClass,
		Status:            rec.Status,
		Superseded:        best,
		SupersededIDs:     out.Superseded,
		SimilarExisting:   out.Similar,
		Message:           msg,
		LinkedThreadCount: out.Linked,
		UnlinkedThreadIDs: out.Unlinked,
	}), nil, nil
}

// missingFrom returns entries of want not present in got.
func missingFrom(want, got []string) []string {
	present := make(map[string]struct{}, len(got))
	for _, g := range got {
		present[g] = struct{}{}
	}
	var missing []string
	for _, w := range want {
		if _, ok := present[w]; !ok {
			missing = append(missing, w)
		}
	}
	return missing
}

// memoryCaptureSchema is the JSON Schema for the memory_capture tool input.
var memoryCaptureSchema = json.RawMessage(`{
  "type": "object",
  "required": ["type", "content"],
  "additionalProperties": false,
  "properties": {
    "type": {
      "type": "string",
      "description": "Organizing axis (a hint, not a binding route): personal_preference (your working style/preference) and episodic_event (a one-off event) are live for you immediately; business_knowledge (a durable business fact), schema_entity (knowledge about a specific dataset/column, with entity_urns), and operational_rule (a how-to-operate rule) enter review for promotion to shared knowledge. The promotion destination (a DataHub catalog entity vs a knowledge page) is chosen at apply time, suggested by whether the insight carries entity_urns; it is not frozen here."
    },
    "content": {"type": "string", "description": "The knowledge to record (10-4000 chars)."},
    "category": {"type": "string", "description": "Optional sub-type: correction, business_context, data_quality, usage_guidance, relationship, enhancement, general (default business_context)."},
    "entity_urns": {"type": "array", "items": {"type": "string"}, "description": "DataHub URNs this capture is about (schema_entity); max 10."},
    "related_columns": {"type": "array", "items": {"type": "object"}, "description": "Optional columns this capture relates to; max 20."},
    "suggested_actions": {"type": "array", "description": "Optional proposed catalog changes (schema_entity, max 5), applied later via apply_knowledge.", "items": {"type": "object"}},
    "confidence": {"type": "string", "description": "high, medium, or low (default medium)."},
    "source": {"type": "string", "description": "user (default), agent_discovery, or enrichment_gap."},
    "thread_ids": {"type": "array", "items": {"type": "string"}, "description": "Optional feedback threads this capture resolves (reviewed sink-classes only)."},
    "metadata": {"type": "object", "description": "Optional free-form metadata."}
  }
}`)
