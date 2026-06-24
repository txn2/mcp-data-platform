package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// registerFeedbackInsightRoutes wires the "capture feedback as an insight" path
// (#662 Phase 2). It is registered only when both a thread store and a memory
// writer are configured.
func (h *Handler) registerFeedbackInsightRoutes() {
	if h.deps.ThreadStore == nil || h.deps.MemoryWriter == nil {
		return
	}
	h.mux.HandleFunc("POST /api/v1/portal/threads/{id}/insight", h.captureThreadInsight)
}

// captureInsightRequest is the optional override payload. With an empty body the
// insight content is derived from the thread (title plus first comment) and the
// sink class defaults to business_knowledge.
type captureInsightRequest struct {
	Content    string   `json:"content,omitempty"`
	Category   string   `json:"category,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	SinkClass  string   `json:"sink_class,omitempty"`
	EntityURNs []string `json:"entity_urns,omitempty"`
}

// captureInsightResponse reports the created insight and whether the source
// thread was linked (and thereby resolved).
type captureInsightResponse struct {
	InsightID string `json:"insight_id"`
	Status    string `json:"status"`
	Linked    bool   `json:"linked"`
}

// captureThreadInsight handles POST /api/v1/portal/threads/{id}/insight.
//
// It turns a feedback thread into a reviewable insight: a pending,
// knowledge-dimension memory_record that enters the apply_knowledge review
// queue, then links the thread to it (resolving the thread and recording an
// insight_linked event) via the existing bridge. This is how feedback becomes
// actionable knowledge rather than a dead-end comment.
//
// @Summary      Capture a feedback thread as an insight
// @Description  Creates a pending insight from a feedback thread and links the thread to it. Requires apply_knowledge access.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        id    path  string                 true   "Thread id"
// @Param        body  body  captureInsightRequest  false  "Optional overrides"
// @Success      201  {object}  captureInsightResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/insight [post]
func (h *Handler) captureThreadInsight(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	// Only reviewers (apply_knowledge holders, or admins) promote feedback into
	// the insight queue, the same capability that reviews and applies it.
	if !h.userHasApplyKnowledge(user) {
		writeError(w, http.StatusForbidden, "capturing feedback as an insight requires apply_knowledge access")
		return
	}

	thread, err := h.deps.ThreadStore.GetThread(r.Context(), r.PathValue(kpIDParam))
	if err != nil {
		writeError(w, http.StatusNotFound, errThreadNotFound)
		return
	}
	// The capturer must be able to see the thread's target.
	if !h.canAccessThreadTarget(w, r, user, threadTarget{
		thread.TargetType, thread.AssetID, thread.CollectionID, thread.PromptID, thread.KnowledgePageID,
	}) {
		return
	}

	rec, status, msg := h.buildThreadInsightRecord(r, thread, user)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if err := h.deps.MemoryWriter.Insert(r.Context(), rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create insight")
		return
	}

	// Bridge: link (and resolve) the source thread. A link failure does not
	// undo the insight; the insight already exists in the review queue.
	linked, err := h.deps.ThreadStore.LinkInsight(r.Context(), []string{thread.ID}, rec.ID, user.UserID, user.Email)
	if err != nil {
		slog.Warn("portal: failed to link insight to thread", "insight_id", rec.ID, "thread_id", thread.ID, logKeyError, err)
	}

	writeJSON(w, http.StatusCreated, captureInsightResponse{
		InsightID: rec.ID,
		Status:    memory.InsightStatusPending,
		Linked:    len(linked) > 0,
	})
}

// buildThreadInsightRecord parses the optional override payload, derives and
// validates the insight fields, and returns the memory.Record to insert. On a
// validation failure it returns a non-zero HTTP status and message; on success
// status is 0. Extracted from captureThreadInsight to keep that handler's
// complexity bounded.
func (h *Handler) buildThreadInsightRecord(r *http.Request, thread *Thread, user *User) (rec memory.Record, status int, msg string) {
	var req captureInsightRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return memory.Record{}, http.StatusBadRequest, errInvalidRequestBody
		}
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = h.threadInsightContent(r, thread)
	}
	sinkClass := strings.TrimSpace(req.SinkClass)
	if sinkClass == "" {
		sinkClass = memory.SinkBusinessKnowledge
	}
	if err := memory.ValidateSinkClass(sinkClass); err != nil {
		return memory.Record{}, http.StatusBadRequest, err.Error()
	}
	// The capture path exists to create a REVIEWABLE insight, so the sink class
	// must be a reviewable (knowledge-dimension) one. The live classes
	// (personal_preference, episodic_event) route to other dimensions the
	// apply_knowledge review queue never lists, which would resolve and link the
	// thread to an insight no reviewer could ever see.
	if memory.SinkClassIsLive(sinkClass) {
		return memory.Record{}, http.StatusBadRequest,
			"sink_class must be a reviewable class (business_knowledge, schema_entity, or operational_rule)"
	}
	if err := memory.ValidateContent(content); err != nil {
		return memory.Record{}, http.StatusBadRequest, err.Error()
	}
	if err := memory.ValidateEntityURNs(req.EntityURNs); err != nil {
		return memory.Record{}, http.StatusBadRequest, err.Error()
	}

	return memory.Record{
		ID:         "mem_" + uuid.NewString(),
		CreatedBy:  user.Email,
		Persona:    h.personaName(user),
		Dimension:  memory.SinkClassDimension(sinkClass),
		SinkClass:  sinkClass,
		Content:    content,
		Category:   memory.NormalizeCategory(req.Category),
		Confidence: memory.NormalizeConfidence(req.Confidence),
		Source:     memory.SourceUser,
		EntityURNs: req.EntityURNs,
		Status:     memory.StatusActive,
		Metadata: map[string]any{
			memory.MetaKeyInsightStatus: memory.InsightStatusPending,
		},
	}, 0, ""
}

// threadInsightContent derives the default insight text from a thread: its title
// plus the body of its first comment event (the original feedback). Falls back
// to whichever is present.
func (h *Handler) threadInsightContent(r *http.Request, thread *Thread) string {
	title := strings.TrimSpace(thread.Title)
	var first string
	if events, err := h.deps.ThreadStore.ListEvents(r.Context(), thread.ID); err == nil {
		for _, e := range events {
			if b := strings.TrimSpace(e.Body); b != "" {
				first = b
				break
			}
		}
	}
	switch {
	case title != "" && first != "":
		return title + "\n\n" + first
	case first != "":
		return first
	default:
		return title
	}
}

// personaName resolves the caller's persona for stamping a captured insight,
// the same way GET /me resolves it. Empty when no persona resolves.
func (h *Handler) personaName(user *User) string {
	if h.deps.PersonaResolver == nil {
		return ""
	}
	if info := h.deps.PersonaResolver(user.Roles); info != nil {
		return info.Name
	}
	return ""
}
