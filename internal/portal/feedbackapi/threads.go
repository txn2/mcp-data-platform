package feedbackapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

const (
	paramTargetType      = "target_type"
	paramAssetID         = "asset_id"
	paramCollectionID    = "collection_id"
	paramPromptID        = "prompt_id"
	paramKnowledgePageID = "knowledge_page_id"
	paramKind            = "kind"
	paramStatus          = "status"
	paramIDs             = "ids"

	maxThreadCountIDs = 200

	errThreadNotFound = "thread not found"
	errThreadScope    = "specify target_type=standalone or exactly one of asset_id, collection_id, prompt_id"
)

// --- request/response types ---

type createThreadRequest struct {
	Kind               string          `json:"kind"`
	TargetType         string          `json:"target_type"`
	AssetID            string          `json:"asset_id"`
	CollectionID       string          `json:"collection_id"`
	PromptID           string          `json:"prompt_id"`
	KnowledgePageID    string          `json:"knowledge_page_id"`
	Anchor             json.RawMessage `json:"anchor" swaggertype:"object"`
	TargetVersion      int             `json:"target_version"`
	Title              string          `json:"title"`
	RequiresResolution bool            `json:"requires_resolution"`
	Body               string          `json:"body"`
	Rating             *int            `json:"rating"`
}

type appendEventRequest struct {
	EventType     string `json:"event_type"`
	Body          string `json:"body"`
	Rating        *int   `json:"rating"`
	ParentEventID string `json:"parent_event_id"`
}

type updateThreadRequest struct {
	Status             *string `json:"status"`
	RequiresResolution *bool   `json:"requires_resolution"`
	ValidationState    *string `json:"validation_state"`
}

// --- handlers ---

// listThreads handles GET /api/v1/portal/threads. The caller must scope the
// query to a single target (an object id or target_type=standalone).
//
// @Summary      List feedback threads
// @Description  Lists feedback threads scoped to a single target (asset_id, collection_id, prompt_id, or target_type=standalone). Standalone threads are visible to any authenticated user; object threads require view access to the target.
// @Tags         Feedback
// @Produce      json
// @Param        target_type    query  string  false  "Target type (use 'standalone' for the shared channel)"
// @Param        asset_id       query  string  false  "Asset target id"
// @Param        collection_id  query  string  false  "Collection target id"
// @Param        prompt_id      query  string  false  "Prompt target id"
// @Param        kind           query  string  false  "Filter by kind"
// @Param        status         query  string  false  "Filter by status"
// @Param        limit          query  int     false  "Page size"
// @Param        offset         query  int     false  "Page offset"
// @Success      200  {object}  pagedResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads [get]
func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	filter := threads.ThreadFilter{
		TargetType:      r.URL.Query().Get(paramTargetType),
		AssetID:         r.URL.Query().Get(paramAssetID),
		CollectionID:    r.URL.Query().Get(paramCollectionID),
		PromptID:        r.URL.Query().Get(paramPromptID),
		KnowledgePageID: r.URL.Query().Get(paramKnowledgePageID),
		Kind:            r.URL.Query().Get(paramKind),
		Status:          r.URL.Query().Get(paramStatus),
		Limit:           intParam(r, paramLimit, threads.DefaultThreadLimit),
		Offset:          intParam(r, paramOffset, 0),
	}

	targetType, ok := scopeFromFilter(filter)
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, errThreadScope)
		return
	}
	filter.TargetType = targetType
	if !h.canAccessThreadTarget(w, r, user, threadTarget{targetType, filter.AssetID, filter.CollectionID, filter.PromptID, filter.KnowledgePageID}) {
		return
	}

	found, total, err := h.cfg.Threads.ListThreads(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list threads")
		return
	}
	if found == nil {
		found = []threads.ThreadWithMeta{}
	}
	httpjson.WriteJSON(w, http.StatusOK, pagedResponse{
		Data: found, Total: total, Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

// createThread handles POST /api/v1/portal/threads.
//
// @Summary      Create a feedback thread
// @Description  Opens a new feedback thread (and its first event) on an asset, collection, prompt, knowledge page, or the standalone channel.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        body  body  createThreadRequest  true  "Thread to create"
// @Success      201  {object}  threads.Thread
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads [post]
func (h *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	var req createThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !threads.ValidThreadKind(req.Kind) {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid kind")
		return
	}
	if !validThreadTarget(req.TargetType, req.AssetID, req.CollectionID, req.PromptID, req.KnowledgePageID) {
		httpjson.WriteError(w, http.StatusBadRequest, errThreadScope)
		return
	}
	if !h.canAccessThreadTarget(w, r, user, threadTarget{req.TargetType, req.AssetID, req.CollectionID, req.PromptID, req.KnowledgePageID}) {
		return
	}

	thread := threads.Thread{
		ID:                 threads.NewThreadID("thr"),
		Kind:               req.Kind,
		TargetType:         req.TargetType,
		AssetID:            req.AssetID,
		CollectionID:       req.CollectionID,
		PromptID:           req.PromptID,
		KnowledgePageID:    req.KnowledgePageID,
		Anchor:             req.Anchor,
		TargetVersion:      req.TargetVersion,
		Title:              req.Title,
		AuthorID:           user.UserID,
		AuthorEmail:        user.Email,
		Status:             threads.ThreadStatusOpen,
		RequiresResolution: req.RequiresResolution,
	}
	first := threads.ThreadEvent{
		ID:          threads.NewThreadID("evt"),
		ThreadID:    thread.ID,
		EventType:   threads.DeriveFirstEventType(req.Kind),
		AuthorID:    user.UserID,
		AuthorEmail: user.Email,
		Body:        req.Body,
		Rating:      req.Rating,
	}

	mentions := h.resolveMentions(r.Context(), &thread, req.Body, user.Email)
	created, err := h.cfg.Threads.CreateThread(r.Context(), thread, stampMentions(first, mentions))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to create thread")
		return
	}
	h.notifyThreadEvent(r.Context(), created, user.Email, req.Body, mentions)
	httpjson.WriteJSON(w, http.StatusCreated, created)
}

// getThread handles GET /api/v1/portal/threads/{id}.
//
// @Summary      Get a feedback thread
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  threads.Thread
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id} [get]
func (h *Handler) getThread(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, thread)
}

// listThreadEvents handles GET /api/v1/portal/threads/{id}/events.
//
// @Summary      List thread events
// @Description  Returns a thread's event timeline (oldest first).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  map[string][]threads.ThreadEvent
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/events [get]
func (h *Handler) listThreadEvents(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}
	events, err := h.cfg.Threads.ListEvents(r.Context(), thread.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	if events == nil {
		events = []threads.ThreadEvent{}
	}
	httpjson.WriteJSON(w, http.StatusOK, map[string]any{"data": events})
}

// threadChainChangeset is the changeset view surfaced on a thread's chain.
type threadChainChangeset struct {
	ID         string    `json:"id"`
	TargetURN  string    `json:"target_urn"`
	ChangeType string    `json:"change_type"`
	CreatedAt  time.Time `json:"created_at"`
	RolledBack bool      `json:"rolled_back"`
}

// threadChainResponse is the resolved knowledge chain for a thread: the insight
// it was captured into and the changeset(s) that applied that insight.
type threadChainResponse struct {
	ThreadID   string                 `json:"thread_id"`
	InsightID  string                 `json:"insight_id,omitempty"`
	Changesets []threadChainChangeset `json:"changesets"`
}

// getThreadChain handles GET /api/v1/portal/threads/{id}/chain.
//
// @Summary      Resolve a thread's knowledge chain
// @Description  Returns the insight a thread was captured into and the changeset(s) that applied it (thread -> insight -> changeset -> target_urn).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  threadChainResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/chain [get]
func (h *Handler) getThreadChain(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}
	resp := threadChainResponse{
		ThreadID:   thread.ID,
		InsightID:  thread.InsightID,
		Changesets: []threadChainChangeset{},
	}
	if thread.InsightID != "" && h.cfg.Changesets != nil {
		changesets, _, err := h.cfg.Changesets.ListChangesets(r.Context(),
			knowledge.ChangesetFilter{SourceInsightID: thread.InsightID})
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "failed to load changesets")
			return
		}
		for _, cs := range changesets {
			resp.Changesets = append(resp.Changesets, threadChainChangeset{
				ID:         cs.ID,
				TargetURN:  cs.TargetURN,
				ChangeType: cs.ChangeType,
				CreatedAt:  cs.CreatedAt,
				RolledBack: cs.RolledBack,
			})
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, resp)
}

// appendThreadEvent handles POST /api/v1/portal/threads/{id}/events.
//
// @Summary      Add a thread event
// @Description  Appends a conversational event (comment, rating, approval, rejection) to a thread.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Thread ID"
// @Param        body  body  appendEventRequest  true  "Event to append"
// @Success      201  {object}  threads.ThreadEvent
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/events [post]
func (h *Handler) appendThreadEvent(w http.ResponseWriter, r *http.Request) {
	user, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}

	var req appendEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	eventType := req.EventType
	if eventType == "" {
		eventType = threads.EventTypeComment
	}
	if !validAppendEventType(eventType) {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid event_type")
		return
	}

	mentions := h.resolveMentions(r.Context(), thread, req.Body, user.Email)
	created, err := h.cfg.Threads.AppendEvent(r.Context(), stampMentions(threads.ThreadEvent{
		ID:            threads.NewThreadID("evt"),
		ThreadID:      thread.ID,
		EventType:     eventType,
		AuthorID:      user.UserID,
		AuthorEmail:   user.Email,
		Body:          req.Body,
		Rating:        req.Rating,
		ParentEventID: req.ParentEventID,
	}, mentions))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to append event")
		return
	}
	h.notifyThreadEvent(r.Context(), thread, user.Email, req.Body, mentions)
	httpjson.WriteJSON(w, http.StatusCreated, created)
}

// updateThread handles PATCH /api/v1/portal/threads/{id} (status/resolution).
//
// @Summary      Update a feedback thread
// @Description  Changes a thread's status, requires_resolution, or validation_state. A status change records a timeline event. Allowed for the thread author, target owner/editor, or an admin.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        id    path  string               true  "Thread ID"
// @Param        body  body  updateThreadRequest  true  "Fields to update"
// @Success      200  {object}  threads.Thread
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id} [patch]
func (h *Handler) updateThread(w http.ResponseWriter, r *http.Request) {
	user, thread := h.loadThreadForModerate(w, r)
	if thread == nil {
		return
	}

	var req updateThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != nil && !threads.ValidThreadStatus(*req.Status) {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid status")
		return
	}
	// validation_state is owned by the validation lifecycle (#603): it carries an
	// author-only gate, a validation_result event, and re-open-on-dispute. The
	// generic moderator PATCH must not set it directly, or an owner/editor who is
	// not the feedback author could self-validate with none of those invariants.
	if req.ValidationState != nil {
		httpjson.WriteError(w, http.StatusBadRequest,
			"validation_state cannot be set here; use POST /api/v1/portal/threads/{id}/validation")
		return
	}

	if err := h.cfg.Threads.UpdateThread(r.Context(), thread.ID, threads.ThreadUpdate(req), user.UserID, user.Email); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to update thread")
		return
	}

	updated, err := h.cfg.Threads.GetThread(r.Context(), thread.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to load updated thread")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, updated)
}

// deleteThread handles DELETE /api/v1/portal/threads/{id}.
//
// @Summary      Delete a feedback thread
// @Description  Soft-deletes a thread. Allowed for the thread author, target owner, or an admin.
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id} [delete]
func (h *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForModerate(w, r)
	if thread == nil {
		return
	}
	if err := h.cfg.Threads.SoftDeleteThread(r.Context(), thread.ID); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to delete thread")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, map[string]string{"status": statusDeleted})
}

// threadCounts handles GET /api/v1/portal/threads/counts, returning the number
// of open threads per target id for list-page badges. Results are scoped to the
// caller: non-admins receive counts only for objects they own, so the endpoint
// never discloses thread counts for objects the caller cannot see.
//
// @Summary      Count open threads per target
// @Description  Returns a map of target id to open-thread count for list-page badges. target_type is asset or collection. Non-admins receive counts only for objects they own.
// @Tags         Feedback
// @Produce      json
// @Param        target_type  query  string  true  "Target type (asset or collection)"
// @Param        ids          query  string  true  "Comma-separated target ids (max 200)"
// @Success      200  {object}  map[string]int
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/counts [get]
func (h *Handler) threadCounts(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	targetType := r.URL.Query().Get(paramTargetType)
	if targetType != portaldomain.TargetTypeAsset && targetType != portaldomain.TargetTypeCollection && targetType != portaldomain.TargetTypeKnowledgePage {
		httpjson.WriteError(w, http.StatusBadRequest, "target_type must be asset, collection, or knowledge_page")
		return
	}
	ids := splitIDs(r.URL.Query().Get(paramIDs))
	// Reject (rather than silently truncate) an oversized id list: truncation
	// would drop badges for owned items past the cap with no signal. The badge
	// caller sends one page of ids, so hitting this means the client is wrong.
	if len(ids) > maxThreadCountIDs {
		httpjson.WriteError(w, http.StatusBadRequest, "too many ids")
		return
	}
	// Knowledge pages are org-shared, so every authenticated user sees their
	// feedback counts; only the owner-scoped asset/collection counts are filtered.
	if !h.userIsAdmin(user) && targetType != portaldomain.TargetTypeKnowledgePage {
		ids = h.filterOwnedTargets(r, targetType, ids, user)
	}

	counts, err := h.cfg.Threads.CountOpenByTargets(r.Context(), targetType, ids)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count threads")
		return
	}
	if counts == nil {
		counts = map[string]int{}
	}
	httpjson.WriteJSON(w, http.StatusOK, counts)
}

// filterOwnedTargets returns the subset of ids the user owns.
func (h *Handler) filterOwnedTargets(r *http.Request, targetType string, ids []string, user *access.User) []string {
	if len(ids) == 0 {
		return ids
	}
	return h.access.OwnedTargetIDs(r.Context(), targetType, ids, user)
}

// splitIDs splits a comma-separated id list, trimming blanks.
func splitIDs(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- access helpers ---

// loadThreadForRead loads the thread named in the path and verifies the caller
// may view its target. Returns (nil, nil) and writes an error on failure.
func (h *Handler) loadThreadForRead(w http.ResponseWriter, r *http.Request) (*access.User, *threads.Thread) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	thread, err := h.cfg.Threads.GetThread(r.Context(), r.PathValue(pathKeyID))
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, errThreadNotFound)
		return nil, nil
	}
	if !h.canAccessThreadTarget(w, r, user, threadTarget{thread.TargetType, thread.AssetID, thread.CollectionID, thread.PromptID, thread.KnowledgePageID}) {
		return nil, nil
	}
	return user, thread
}

// loadThreadForModerate loads the thread and verifies the caller may moderate
// it (thread author, target owner/editor, or admin).
func (h *Handler) loadThreadForModerate(w http.ResponseWriter, r *http.Request) (*access.User, *threads.Thread) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	thread, err := h.cfg.Threads.GetThread(r.Context(), r.PathValue(pathKeyID))
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, errThreadNotFound)
		return nil, nil
	}
	if !h.canModerateThread(r, user, thread) {
		httpjson.WriteError(w, http.StatusForbidden, "only the author, target owner, or an admin can modify this thread")
		return nil, nil
	}
	return user, thread
}

// canAccessThreadTarget reports whether the user may read/author feedback on the
// given target, writing an HTTP error on denial. Standalone is open to any
// authenticated user; object targets require view access to the object.
func (h *Handler) canAccessThreadTarget(w http.ResponseWriter, r *http.Request, user *access.User, t threadTarget) bool {
	switch t.kind {
	case portaldomain.TargetTypeStandalone:
		return true
	case portaldomain.TargetTypeAsset:
		return h.threadAssetAccess(w, r, user, t.asset)
	case portaldomain.TargetTypeCollection:
		return h.threadCollectionAccess(w, r, user, t.collection)
	case portaldomain.TargetTypePrompt:
		return h.threadPromptAccess(w, r, user, t.prompt)
	case portaldomain.TargetTypeKnowledgePage:
		return h.threadKnowledgePageAccess(w, r, t.knowledgePage)
	default:
		httpjson.WriteError(w, http.StatusBadRequest, errThreadScope)
		return false
	}
}

// threadKnowledgePageAccess allows any authenticated user to read and add
// feedback on a knowledge page: pages are org-shared canonical knowledge, so
// view access is universal. It only verifies the page exists and is not deleted.
// Moderation (status change, delete) is gated separately in canModerateThread.
func (h *Handler) threadKnowledgePageAccess(w http.ResponseWriter, r *http.Request, pageID string) bool {
	if h.cfg.KnowledgePages == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "knowledge pages not configured")
		return false
	}
	page, err := h.cfg.KnowledgePages.Get(r.Context(), pageID)
	if errors.Is(err, knowledgepage.ErrNotFound) || (err == nil && page.DeletedAt != nil) {
		httpjson.WriteError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
		return false
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to verify knowledge page")
		return false
	}
	return true
}

func (h *Handler) threadAssetAccess(w http.ResponseWriter, r *http.Request, user *access.User, assetID string) bool {
	asset, err := h.cfg.Assets.Get(r.Context(), assetID)
	if err != nil || asset.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, errAssetNotFound)
		return false
	}
	if h.userIsAdmin(user) {
		return true
	}
	return h.assetViewable(w, r, assetID, asset, user)
}

func (h *Handler) threadCollectionAccess(w http.ResponseWriter, r *http.Request, user *access.User, collectionID string) bool {
	if h.cfg.Collections == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "collections not configured")
		return false
	}
	coll, err := h.cfg.Collections.Get(r.Context(), collectionID)
	if err != nil || coll.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, errCollectionNotFound)
		return false
	}
	if h.userIsAdmin(user) || coll.OwnerID == user.UserID || h.access.CollectionSharePermission(r.Context(), collectionID, user) != "" {
		return true
	}
	httpjson.WriteError(w, http.StatusForbidden, errAccessDenied)
	return false
}

func (h *Handler) threadPromptAccess(w http.ResponseWriter, r *http.Request, user *access.User, promptID string) bool {
	if h.cfg.Prompts == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "prompts not configured")
		return false
	}
	pr, err := h.cfg.Prompts.GetByID(r.Context(), promptID)
	if err != nil || pr == nil {
		httpjson.WriteError(w, http.StatusNotFound, "prompt not found")
		return false
	}
	if h.access.CanViewPrompt(r.Context(), user, pr) {
		return true
	}
	httpjson.WriteError(w, http.StatusForbidden, errAccessDenied)
	return false
}

// canModerateThread reports whether the user may change a thread's status or
// delete it: the thread author, an admin, or an owner/editor of the target.
func (h *Handler) canModerateThread(r *http.Request, user *access.User, thread *threads.Thread) bool {
	return h.access.CanModerateThread(r.Context(), user, thread)
}

func (h *Handler) userIsAdmin(user *access.User) bool {
	return h.access.IsAdmin(user)
}

// --- small helpers ---

// threadTarget bundles a thread's target discriminator and the 1-of-N object
// ids, so access checks take one value instead of four positional args.
type threadTarget struct {
	kind          string
	asset         string
	collection    string
	prompt        string
	knowledgePage string
}

// countSet returns how many of the given ids are non-empty.
func countSet(ids ...string) int {
	n := 0
	for _, id := range ids {
		if id != "" {
			n++
		}
	}
	return n
}

// scopeFromFilter validates that a list filter is scoped to exactly one target
// and returns the resolved target_type.
func scopeFromFilter(f threads.ThreadFilter) (string, bool) {
	n := countSet(f.AssetID, f.CollectionID, f.PromptID, f.KnowledgePageID)
	if f.TargetType == portaldomain.TargetTypeStandalone {
		return portaldomain.TargetTypeStandalone, n == 0 // standalone must carry no object target
	}
	if n != 1 {
		return "", false
	}
	switch {
	case f.AssetID != "":
		return portaldomain.TargetTypeAsset, true
	case f.CollectionID != "":
		return portaldomain.TargetTypeCollection, true
	case f.KnowledgePageID != "":
		return portaldomain.TargetTypeKnowledgePage, true
	default:
		return portaldomain.TargetTypePrompt, true
	}
}

// validThreadTarget reports whether a create request names a valid 1-of-N (or
// standalone) target: standalone carries no object id, every other type carries
// exactly its own object id and no other.
func validThreadTarget(targetType, assetID, collectionID, promptID, knowledgePageID string) bool {
	n := countSet(assetID, collectionID, promptID, knowledgePageID)
	if targetType == portaldomain.TargetTypeStandalone {
		return n == 0
	}
	objectIDs := map[string]string{
		portaldomain.TargetTypeAsset:         assetID,
		portaldomain.TargetTypeCollection:    collectionID,
		portaldomain.TargetTypePrompt:        promptID,
		portaldomain.TargetTypeKnowledgePage: knowledgePageID,
	}
	id, ok := objectIDs[targetType]
	return ok && n == 1 && id != ""
}

// validAppendEventType limits client-authored events to conversational kinds;
// status/resolution and knowledge-link events are produced by the system.
func validAppendEventType(eventType string) bool {
	switch eventType {
	case threads.EventTypeComment, threads.EventTypeRating, threads.EventTypeApproval, threads.EventTypeRejection:
		return true
	default:
		return false
	}
}
