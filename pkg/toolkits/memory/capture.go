package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
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

// RecallQuery is the recall-first lookup: the embedding of the candidate
// content, the entities it concerns, and the caller's email, plus the
// cosine threshold above which a prior record counts as similar. The check
// runs when the capture's embedding is written (SettleRecall), so it never
// runs without one.
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
	// CreatedAt orders a restating pair: the newer record supersedes the
	// older, whichever of the two is embedded first (#1987).
	CreatedAt time.Time `json:"-"`
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

// IndexNotifier asks for a stored record to be embedded now rather than on the
// next reconciler sweep. Satisfied by *indexjobs.Producer; declared here so the
// toolkit does not import the queue.
type IndexNotifier interface {
	NotifyWrite(ctx context.Context, sourceID string)
}

// SetRecallChecker wires the recall-first checker.
func (t *Toolkit) SetRecallChecker(rc RecallChecker) { t.recallChecker = rc }

// SetIndexNotifier wires the write-path enqueue that embeds a stored capture
// or an updated record off the request (#1987).
func (t *Toolkit) SetIndexNotifier(n IndexNotifier) { t.indexNotifier = n }

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
	Sources          []string                 `json:"sources,omitempty"`
	Metadata         map[string]any           `json:"metadata,omitempty"`
}

// The recall_check values a capture response reports.
const (
	// recallCheckPending: the record is stored and queued for embedding, and
	// the recall-first check runs when the embedding is written.
	recallCheckPending = memstore.RecallCheckPending
	// recallCheckUnavailable: no embedding provider or no recall checker, so
	// no check will run and the capture simply appends.
	recallCheckUnavailable = "unavailable"
)

// memoryCaptureOutput is the memory_capture success response. The capture is
// stored before it is embedded (#1987), so the response cannot say what it
// superseded: RecallCheck says whether and when that check runs.
type memoryCaptureOutput struct {
	ID          string `json:"id"`
	SinkClass   string `json:"sink_class"`
	Status      string `json:"status"`
	RecallCheck string `json:"recall_check"`
	Message     string `json:"message"`
	// LinkedThreadCount and UnlinkedThreadIDs report the feedback threads a
	// reviewed capture was linked to (#602).
	LinkedThreadCount int      `json:"linked_thread_count,omitempty"`
	UnlinkedThreadIDs []string `json:"unlinked_thread_ids,omitempty"`
}

// handleMemoryCapture is the unified write verb. It validates the input, stores
// the record and queues its embedding, and returns without waiting on the
// embedder (#1987); the recall-first check runs when the embedding is written.
// It routes by sink-class: live classes (personal_preference, episodic_event) are
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
	RecallCheck string
	Linked      int
	Unlinked    []string
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
// insert, queue the embedding, then thread-link. It never calls the embedder: a
// slow embedder used to hold a committed write open past the client's timeout,
// and the caller retried a capture that had been stored (#1987). The record is
// marked for the recall-first check, which SettleRecall runs when the index job
// writes the embedding. Both the memory_capture tool and AutoCapture funnel
// through here so server-initiated captures get identical semantics.
func (t *Toolkit) applyCapture(ctx context.Context, rec *memstore.Record, sinkClass string, actor captureActor, threadIDs []string) (captureOutcome, error) {
	// Both write paths converge here, so this is where the record is made
	// equal to what it means: a record is about an entity once, and a repeat
	// would silently drop out of every list that keys on the URN.
	rec.EntityURNs = memstore.NormalizeEntityURNs(rec.EntityURNs)
	recall := recallCheckUnavailable
	if t.recallChecker != nil && embedding.IsConfigured(t.embedder) {
		recall = recallCheckPending
		rec.Metadata = withRecallPending(rec.Metadata)
	}

	if err := t.store.Insert(ctx, *rec); err != nil {
		return captureOutcome{}, fmt.Errorf("insert capture: %w", err)
	}
	t.notifyIndex(ctx, rec.ID)

	linked, unlinked := t.linkCaptureThreads(ctx, actor, rec.ID, sinkClass, threadIDs)
	return captureOutcome{RecallCheck: recall, Linked: linked, Unlinked: unlinked}, nil
}

// withRecallPending returns meta with the capture marked for the recall-first
// check, copying rather than mutating a map the caller may still hold.
func withRecallPending(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta))
	maps.Copy(out, meta)
	out[memstore.MetaKeyRecallCheck] = memstore.RecallCheckPending
	return out
}

// notifyIndex queues the record's embedding. Best-effort: with no notifier
// wired the reconciler still finds the NULL embedding on its next sweep.
func (t *Toolkit) notifyIndex(ctx context.Context, id string) {
	if t.indexNotifier != nil {
		t.indexNotifier.NotifyWrite(ctx, id)
	}
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

// withSources folds the cited calls into the free-form metadata, so the one
// metadata assembler sees them alongside everything else a capture carries.
func withSources(input memoryCaptureInput) map[string]any {
	if len(input.Sources) == 0 {
		return input.Metadata
	}
	extra := map[string]any{}
	maps.Copy(extra, input.Metadata)
	extra[memstore.MetaKeySources] = input.Sources
	return extra
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
		Metadata:       captureMetadata(input.Type, actor.SessionID, input.SuggestedActions, withSources(input)),
	}
}

// maxCaptureSources bounds how many calls one capture may confirm. A capture
// states what answered a question; a list longer than this is not a statement.
const maxCaptureSources = 20

// captureMetadata builds the record metadata, adding the pending insight overlay
// (review state + catalog proposals + session) for reviewed sink-classes so
// apply_knowledge surfaces them as pending insights, and the calls the capture
// confirms. Identity-agnostic (takes a sessionID, not a PlatformContext) so both
// the tool and AutoCapture share it.
func captureMetadata(sinkClass, sessionID string, suggestedActions []suggestedActionInput, extra map[string]any) map[string]any {
	meta := map[string]any{}
	maps.Copy(meta, extra)
	// The normalized list replaces whatever form the caller sent, and a list
	// that normalizes to nothing leaves no key: the catalog matches on the
	// reference form, so a raw value left here would name no call.
	if sources := normalizeSources(extra); len(sources) > 0 {
		meta[memstore.MetaKeySources] = sources
	} else {
		delete(meta, memstore.MetaKeySources)
	}
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

// normalizeSources reads the calls a capture confirms out of the metadata it was
// assembled with, and returns them in the one reference form the catalog matches
// on. A bare event id is accepted and expanded: the id is what a tool result
// hands back as call_id, and refusing it would be refusing the agent's own
// receipt.
func normalizeSources(extra map[string]any) []string {
	raw, _ := extra[memstore.MetaKeySources].([]string)
	seen := make(map[string]struct{}, len(raw))
	sources := make([]string, 0, len(raw))
	for _, s := range raw {
		id := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), knowledgepage.CallReferencePrefix))
		ref := knowledgepage.CallRef(id)
		if ref == "" {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		sources = append(sources, ref)
		if len(sources) == maxCaptureSources {
			break
		}
	}
	return sources
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
	if out.RecallCheck == recallCheckPending {
		msg += " The recall check runs once the record is embedded, shortly after this call:" +
			" a record this one restates is then marked superseded by it, and records similar to it" +
			" are listed in its metadata as similar_existing, for you to consolidate with memory_manage."
	}
	return toolkit.JSONResult(memoryCaptureOutput{
		ID:                rec.ID,
		SinkClass:         rec.SinkClass,
		Status:            rec.Status,
		RecallCheck:       out.RecallCheck,
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
    "sources": {"type": "array", "items": {"type": "string"}, "description": "The calls this capture confirms, as the mcp:call:<id> reference each query and API invocation returns (call_id in its result). Cite the call whose result answered the question: it records the query as reusable, with your description of what it answers, and puts it in the review queue for promotion to the catalog. Max 20."},
    "metadata": {"type": "object", "description": "Optional free-form metadata."}
  }
}`)
