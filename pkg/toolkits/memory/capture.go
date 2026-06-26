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

// maxSuggestedActions caps the catalog-change proposals a single capture may
// carry, mirroring knowledge.MaxSuggestedActions.
const maxSuggestedActions = 5

// logKeyError is the slog attribute key for errors in this file.
const logKeyError = "error"

// RecallQuery is the recall-first lookup: the precomputed embedding of the
// candidate content, the entities it concerns, and the caller's email, plus the
// cosine threshold above which a prior record counts as a restatement. Embedding
// is empty when no embedder is configured; in that case recall is skipped (no
// reliable similarity, so the capture simply appends).
type RecallQuery struct {
	Embedding   []float32
	EntityURNs  []string
	CallerEmail string
	MinScore    float64
}

// RecallChecker finds an existing record a new capture restates, so the write
// path can supersede instead of appending (recall-first, #633). Implemented by
// the platform over the memory store; declared here so this package does not
// import pkg/knowledge.
type RecallChecker interface {
	ExistingMatch(ctx context.Context, q RecallQuery) (id string, score float64, err error)
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
	ID                string   `json:"id"`
	SinkClass         string   `json:"sink_class"`
	Status            string   `json:"status"`
	Superseded        string   `json:"superseded,omitempty"`
	Message           string   `json:"message"`
	LinkedThreadCount int      `json:"linked_thread_count,omitempty"`
	UnlinkedThreadIDs []string `json:"unlinked_thread_ids,omitempty"`
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
		return errorResult(msg), nil, nil
	}

	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserEmail == "" {
		return errorResult("a user identity (email) is required to capture knowledge"), nil, nil
	}

	id, err := generateID()
	if err != nil {
		return errorResult("failed to generate ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	rec := t.buildCaptureRecord(id, content, input, pc)
	t.embedCaptureRecord(ctx, &rec, content)

	// Recall-first runs BEFORE the insert (so the new row cannot be its own
	// match) and reuses the embedding just computed (no second embed call).
	matchID := t.findPriorMatch(ctx, rec)

	if err := t.store.Insert(ctx, rec); err != nil {
		return errorResult("failed to capture: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	superseded := t.applySupersede(ctx, matchID, rec.ID)
	linked, unlinked := t.linkCaptureThreads(ctx, pc, rec.ID, input.Type, input.ThreadIDs)

	return captureSuccess(rec, superseded, linked, unlinked)
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
func (*Toolkit) buildCaptureRecord(id, content string, input memoryCaptureInput, pc *middleware.PlatformContext) memstore.Record {
	return memstore.Record{
		ID:             id,
		CreatedBy:      pc.UserEmail,
		Persona:        pc.PersonaName,
		Dimension:      memstore.SinkClassDimension(input.Type),
		SinkClass:      input.Type,
		Content:        content,
		Category:       memstore.NormalizeCategory(input.Category),
		Confidence:     memstore.NormalizeConfidence(input.Confidence),
		Source:         memstore.NormalizeSource(input.Source),
		EntityURNs:     input.EntityURNs,
		RelatedColumns: input.RelatedColumns,
		Status:         memstore.StatusActive,
		Metadata:       captureMetadata(input, pc),
	}
}

// captureMetadata builds the record metadata, adding the pending insight overlay
// (review state + catalog proposals + session) for reviewed sink-classes so
// apply_knowledge surfaces them as pending insights.
func captureMetadata(input memoryCaptureInput, pc *middleware.PlatformContext) map[string]any {
	meta := map[string]any{}
	maps.Copy(meta, input.Metadata)
	if !memstore.SinkClassIsLive(input.Type) {
		meta[memstore.MetaKeyInsightStatus] = memstore.InsightStatusPending
		if pc.SessionID != "" {
			meta[memstore.MetaKeySessionID] = pc.SessionID
		}
		if len(input.SuggestedActions) > 0 {
			meta[memstore.MetaKeySuggestedActions] = input.SuggestedActions
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

// findPriorMatch returns the id of an existing record this capture restates, or
// "" when recall is unavailable (no checker, no embedding) or nothing clears the
// threshold. Best-effort: a recall error never fails the capture.
func (t *Toolkit) findPriorMatch(ctx context.Context, rec memstore.Record) string {
	if t.recallChecker == nil || len(rec.Embedding) == 0 {
		return ""
	}
	matchID, _, err := t.recallChecker.ExistingMatch(ctx, RecallQuery{
		Embedding:   rec.Embedding,
		EntityURNs:  rec.EntityURNs,
		CallerEmail: rec.CreatedBy,
		MinScore:    recallSupersedeThreshold,
	})
	if err != nil {
		slog.Debug("memory_capture: recall-first check failed", logKeyError, err)
		return ""
	}
	return matchID
}

// applySupersede marks the prior record superseded by the new capture. Best-
// effort: a failure is logged and the capture still succeeds (the new row is
// already stored), returning "" so the caller does not falsely claim a supersede.
func (t *Toolkit) applySupersede(ctx context.Context, matchID, newID string) string {
	if matchID == "" || matchID == newID {
		return ""
	}
	if err := t.store.Supersede(ctx, matchID, newID); err != nil {
		slog.Warn("memory_capture: failed to supersede prior record", "old", matchID, "new", newID, logKeyError, err)
		return ""
	}
	return matchID
}

// linkCaptureThreads bridges a reviewed capture to feedback threads (#602).
// Thread linking is a review-loop concept, so live captures (and captures with
// no linker wired) surface the thread_ids as unlinked rather than silently
// dropping them.
func (t *Toolkit) linkCaptureThreads(ctx context.Context, pc *middleware.PlatformContext, id, sinkClass string, threadIDs []string) (linked int, unlinked []string) {
	if len(threadIDs) == 0 {
		return 0, nil
	}
	if memstore.SinkClassIsLive(sinkClass) || t.threadLinker == nil {
		return 0, threadIDs
	}
	linkedIDs, err := t.threadLinker.LinkInsight(ctx, threadIDs, id, pc.UserID, pc.UserEmail)
	if err != nil {
		slog.Warn("memory_capture: failed to link threads", "id", id, logKeyError, err)
		return 0, threadIDs
	}
	return len(linkedIDs), missingFrom(threadIDs, linkedIDs)
}

// captureSuccess marshals the success response.
func captureSuccess(rec memstore.Record, superseded string, linked int, unlinked []string) (*mcp.CallToolResult, any, error) {
	msg := "Captured. "
	if memstore.SinkClassIsLive(rec.SinkClass) {
		msg += "Available to you immediately."
	} else {
		msg += "It will be reviewed before promotion to a shared catalog."
	}
	if superseded != "" {
		msg += " A prior record was superseded."
	}
	return jsonResult(memoryCaptureOutput{
		ID:                rec.ID,
		SinkClass:         rec.SinkClass,
		Status:            rec.Status,
		Superseded:        superseded,
		Message:           msg,
		LinkedThreadCount: linked,
		UnlinkedThreadIDs: unlinked,
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
