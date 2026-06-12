package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	paramTargetType   = "target_type"
	paramAssetID      = "asset_id"
	paramCollectionID = "collection_id"
	paramPromptID     = "prompt_id"
	paramKind         = "kind"
	paramStatus       = "status"
	paramIDs          = "ids"

	maxThreadCountIDs = 200

	errThreadNotFound = "thread not found"
	errThreadScope    = "specify target_type=standalone or exactly one of asset_id, collection_id, prompt_id"
)

// registerThreadRoutes wires the feedback thread endpoints. Threads require a
// thread store and the share store (used for object-target visibility checks).
func (h *Handler) registerThreadRoutes() {
	if h.deps.ThreadStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/portal/threads", h.listThreads)
	h.mux.HandleFunc("POST /api/v1/portal/threads", h.createThread)
	h.mux.HandleFunc("GET /api/v1/portal/threads/counts", h.threadCounts)
	h.mux.HandleFunc("GET /api/v1/portal/threads/{id}", h.getThread)
	h.mux.HandleFunc("PATCH /api/v1/portal/threads/{id}", h.updateThread)
	h.mux.HandleFunc("DELETE /api/v1/portal/threads/{id}", h.deleteThread)
	h.mux.HandleFunc("GET /api/v1/portal/threads/{id}/events", h.listThreadEvents)
	h.mux.HandleFunc("GET /api/v1/portal/threads/{id}/chain", h.getThreadChain)
	h.mux.HandleFunc("POST /api/v1/portal/threads/{id}/events", h.appendThreadEvent)
}

// --- request/response types ---

type createThreadRequest struct {
	Kind               string          `json:"kind"`
	TargetType         string          `json:"target_type"`
	AssetID            string          `json:"asset_id"`
	CollectionID       string          `json:"collection_id"`
	PromptID           string          `json:"prompt_id"`
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
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads [get]
func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	filter := ThreadFilter{
		TargetType:   r.URL.Query().Get(paramTargetType),
		AssetID:      r.URL.Query().Get(paramAssetID),
		CollectionID: r.URL.Query().Get(paramCollectionID),
		PromptID:     r.URL.Query().Get(paramPromptID),
		Kind:         r.URL.Query().Get(paramKind),
		Status:       r.URL.Query().Get(paramStatus),
		Limit:        intParam(r, paramLimit, defaultThreadLimit),
		Offset:       intParam(r, paramOffset, 0),
	}

	targetType, ok := scopeFromFilter(filter)
	if !ok {
		writeError(w, http.StatusBadRequest, errThreadScope)
		return
	}
	filter.TargetType = targetType
	if !h.canAccessThreadTarget(w, r, user, threadTarget{targetType, filter.AssetID, filter.CollectionID, filter.PromptID}) {
		return
	}

	threads, total, err := h.deps.ThreadStore.ListThreads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list threads")
		return
	}
	if threads == nil {
		threads = []ThreadWithMeta{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: threads, Total: total, Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

// createThread handles POST /api/v1/portal/threads.
//
// @Summary      Create a feedback thread
// @Description  Opens a new feedback thread (and its first event) on an asset, collection, prompt, or the standalone channel.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        body  body  createThreadRequest  true  "Thread to create"
// @Success      201  {object}  Thread
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads [post]
func (h *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	var req createThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !ValidThreadKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "invalid kind")
		return
	}
	if !validThreadTarget(req.TargetType, req.AssetID, req.CollectionID, req.PromptID) {
		writeError(w, http.StatusBadRequest, errThreadScope)
		return
	}
	if !h.canAccessThreadTarget(w, r, user, threadTarget{req.TargetType, req.AssetID, req.CollectionID, req.PromptID}) {
		return
	}

	thread := Thread{
		ID:                 newThreadID("thr"),
		Kind:               req.Kind,
		TargetType:         req.TargetType,
		AssetID:            req.AssetID,
		CollectionID:       req.CollectionID,
		PromptID:           req.PromptID,
		Anchor:             req.Anchor,
		TargetVersion:      req.TargetVersion,
		Title:              req.Title,
		AuthorID:           user.UserID,
		AuthorEmail:        user.Email,
		Status:             ThreadStatusOpen,
		RequiresResolution: req.RequiresResolution,
	}
	first := ThreadEvent{
		ID:          newThreadID("evt"),
		ThreadID:    thread.ID,
		EventType:   deriveFirstEventType(req.Kind),
		AuthorID:    user.UserID,
		AuthorEmail: user.Email,
		Body:        req.Body,
		Rating:      req.Rating,
	}

	created, err := h.deps.ThreadStore.CreateThread(r.Context(), thread, first)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create thread")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// getThread handles GET /api/v1/portal/threads/{id}.
//
// @Summary      Get a feedback thread
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  Thread
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id} [get]
func (h *Handler) getThread(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

// listThreadEvents handles GET /api/v1/portal/threads/{id}/events.
//
// @Summary      List thread events
// @Description  Returns a thread's event timeline (oldest first).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  map[string][]ThreadEvent
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/events [get]
func (h *Handler) listThreadEvents(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForRead(w, r)
	if thread == nil {
		return
	}
	events, err := h.deps.ThreadStore.ListEvents(r.Context(), thread.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	if events == nil {
		events = []ThreadEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
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
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
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
	if thread.InsightID != "" && h.deps.ChangesetReader != nil {
		changesets, _, err := h.deps.ChangesetReader.ListChangesets(r.Context(),
			knowledge.ChangesetFilter{SourceInsightID: thread.InsightID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load changesets")
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
	writeJSON(w, http.StatusOK, resp)
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
// @Success      201  {object}  ThreadEvent
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
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
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	eventType := req.EventType
	if eventType == "" {
		eventType = EventTypeComment
	}
	if !validAppendEventType(eventType) {
		writeError(w, http.StatusBadRequest, "invalid event_type")
		return
	}

	created, err := h.deps.ThreadStore.AppendEvent(r.Context(), ThreadEvent{
		ID:            newThreadID("evt"),
		ThreadID:      thread.ID,
		EventType:     eventType,
		AuthorID:      user.UserID,
		AuthorEmail:   user.Email,
		Body:          req.Body,
		Rating:        req.Rating,
		ParentEventID: req.ParentEventID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to append event")
		return
	}
	writeJSON(w, http.StatusCreated, created)
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
// @Success      200  {object}  Thread
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
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
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != nil && !ValidThreadStatus(*req.Status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if req.ValidationState != nil && !ValidThreadValidationState(*req.ValidationState) {
		writeError(w, http.StatusBadRequest, "invalid validation_state")
		return
	}

	if err := h.deps.ThreadStore.UpdateThread(r.Context(), thread.ID, ThreadUpdate(req), user.UserID, user.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update thread")
		return
	}

	updated, err := h.deps.ThreadStore.GetThread(r.Context(), thread.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated thread")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteThread handles DELETE /api/v1/portal/threads/{id}.
//
// @Summary      Delete a feedback thread
// @Description  Soft-deletes a thread. Allowed for the thread author, target owner, or an admin.
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Thread ID"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id} [delete]
func (h *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	_, thread := h.loadThreadForModerate(w, r)
	if thread == nil {
		return
	}
	if err := h.deps.ThreadStore.SoftDeleteThread(r.Context(), thread.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete thread")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": statusDeleted})
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
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/counts [get]
func (h *Handler) threadCounts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	targetType := r.URL.Query().Get(paramTargetType)
	if targetType != targetTypeAsset && targetType != targetTypeCollection {
		writeError(w, http.StatusBadRequest, "target_type must be asset or collection")
		return
	}
	ids := splitIDs(r.URL.Query().Get(paramIDs))
	// Reject (rather than silently truncate) an oversized id list: truncation
	// would drop badges for owned items past the cap with no signal. The badge
	// caller sends one page of ids, so hitting this means the client is wrong.
	if len(ids) > maxThreadCountIDs {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	if !h.userIsAdmin(user) {
		ids = h.filterOwnedTargets(r, targetType, ids, user)
	}

	counts, err := h.deps.ThreadStore.CountOpenByTargets(r.Context(), targetType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count threads")
		return
	}
	if counts == nil {
		counts = map[string]int{}
	}
	writeJSON(w, http.StatusOK, counts)
}

// filterOwnedTargets returns the subset of ids the user owns. Assets are
// resolved in one batch; collections are resolved per id (a user has few).
func (h *Handler) filterOwnedTargets(r *http.Request, targetType string, ids []string, user *User) []string {
	if len(ids) == 0 {
		return ids
	}
	switch targetType {
	case targetTypeAsset:
		return h.ownedAssetIDs(r, ids, user)
	case targetTypeCollection:
		return h.ownedCollectionIDs(r, ids, user)
	default:
		return nil
	}
}

func (h *Handler) ownedAssetIDs(r *http.Request, ids []string, user *User) []string {
	if h.deps.AssetStore == nil {
		return nil
	}
	assets, err := h.deps.AssetStore.GetByIDs(r.Context(), ids)
	if err != nil {
		return nil
	}
	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		if a, ok := assets[id]; ok && a != nil && a.DeletedAt == nil && a.OwnerID == user.UserID {
			owned = append(owned, id)
		}
	}
	return owned
}

func (h *Handler) ownedCollectionIDs(r *http.Request, ids []string, user *User) []string {
	if h.deps.CollectionStore == nil {
		return nil
	}
	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		coll, err := h.deps.CollectionStore.Get(r.Context(), id)
		if err == nil && coll.DeletedAt == nil && coll.OwnerID == user.UserID {
			owned = append(owned, id)
		}
	}
	return owned
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
func (h *Handler) loadThreadForRead(w http.ResponseWriter, r *http.Request) (*User, *Thread) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	thread, err := h.deps.ThreadStore.GetThread(r.Context(), r.PathValue(pathKeyID))
	if err != nil {
		writeError(w, http.StatusNotFound, errThreadNotFound)
		return nil, nil
	}
	if !h.canAccessThreadTarget(w, r, user, threadTarget{thread.TargetType, thread.AssetID, thread.CollectionID, thread.PromptID}) {
		return nil, nil
	}
	return user, thread
}

// loadThreadForModerate loads the thread and verifies the caller may moderate
// it (thread author, target owner/editor, or admin).
func (h *Handler) loadThreadForModerate(w http.ResponseWriter, r *http.Request) (*User, *Thread) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	thread, err := h.deps.ThreadStore.GetThread(r.Context(), r.PathValue(pathKeyID))
	if err != nil {
		writeError(w, http.StatusNotFound, errThreadNotFound)
		return nil, nil
	}
	if !h.canModerateThread(r, user, thread) {
		writeError(w, http.StatusForbidden, "only the author, target owner, or an admin can modify this thread")
		return nil, nil
	}
	return user, thread
}

// canAccessThreadTarget reports whether the user may read/author feedback on the
// given target, writing an HTTP error on denial. Standalone is open to any
// authenticated user; object targets require view access to the object.
func (h *Handler) canAccessThreadTarget(w http.ResponseWriter, r *http.Request, user *User, t threadTarget) bool {
	switch t.kind {
	case targetTypeStandalone:
		return true
	case targetTypeAsset:
		return h.threadAssetAccess(w, r, user, t.asset)
	case targetTypeCollection:
		return h.threadCollectionAccess(w, r, user, t.collection)
	case targetTypePrompt:
		return h.threadPromptAccess(w, r, user, t.prompt)
	default:
		writeError(w, http.StatusBadRequest, errThreadScope)
		return false
	}
}

func (h *Handler) threadAssetAccess(w http.ResponseWriter, r *http.Request, user *User, assetID string) bool {
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil || asset.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return false
	}
	if h.userIsAdmin(user) {
		return true
	}
	return h.canViewAsset(w, r, assetID, asset, user)
}

func (h *Handler) threadCollectionAccess(w http.ResponseWriter, r *http.Request, user *User, collectionID string) bool {
	if h.deps.CollectionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "collections not configured")
		return false
	}
	coll, err := h.deps.CollectionStore.Get(r.Context(), collectionID)
	if err != nil || coll.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return false
	}
	if h.userIsAdmin(user) || coll.OwnerID == user.UserID || h.collectionSharePermission(r, collectionID, user) != "" {
		return true
	}
	writeError(w, http.StatusForbidden, errAccessDenied)
	return false
}

func (h *Handler) threadPromptAccess(w http.ResponseWriter, r *http.Request, user *User, promptID string) bool {
	if h.deps.PromptStore == nil {
		writeError(w, http.StatusServiceUnavailable, "prompts not configured")
		return false
	}
	pr, err := h.deps.PromptStore.GetByID(r.Context(), promptID)
	if err != nil || pr == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return false
	}
	if h.userCanViewPrompt(r, user, pr) {
		return true
	}
	writeError(w, http.StatusForbidden, errAccessDenied)
	return false
}

// userCanViewPrompt reports whether the user can see the prompt: global prompts
// are visible to all; personal prompts to their owner, admins, or share grantees.
func (h *Handler) userCanViewPrompt(r *http.Request, user *User, pr *prompt.Prompt) bool {
	if pr.Scope != prompt.ScopePersonal {
		return true
	}
	if h.userIsAdmin(user) || (pr.OwnerEmail != "" && strings.EqualFold(pr.OwnerEmail, user.Email)) {
		return true
	}
	refs, err := h.deps.ShareStore.ListSharedPromptsWithUser(r.Context(), user.UserID, user.Email)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if ref.PromptID == pr.ID {
			return true
		}
	}
	return false
}

// canModerateThread reports whether the user may change a thread's status or
// delete it: the thread author, an admin, or an owner/editor of the target.
// Standalone threads are moderated only by their author or an admin.
func (h *Handler) canModerateThread(r *http.Request, user *User, thread *Thread) bool {
	if h.userIsAdmin(user) || thread.AuthorID == user.UserID {
		return true
	}
	switch thread.TargetType {
	case targetTypeAsset:
		return h.canEditAssetSilent(r, thread.AssetID, user)
	case targetTypeCollection:
		return h.canEditCollectionSilent(r, thread.CollectionID, user)
	case targetTypePrompt:
		return h.ownsPersonalPrompt(r, thread.PromptID, user)
	default:
		return false // standalone: only author/admin (handled above)
	}
}

// canEditAssetSilent reports owner-or-editor access to an asset without writing
// an HTTP response (the *Silent helpers back moderation checks).
func (h *Handler) canEditAssetSilent(r *http.Request, assetID string, user *User) bool {
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil || asset.DeletedAt != nil {
		return false
	}
	if asset.OwnerID == user.UserID {
		return true
	}
	perm, _ := h.sharePermissionForUser(r, assetID, user)
	return perm == PermissionEditor
}

// canEditCollectionSilent reports owner-or-editor access to a collection.
func (h *Handler) canEditCollectionSilent(r *http.Request, collectionID string, user *User) bool {
	if h.deps.CollectionStore == nil {
		return false
	}
	coll, err := h.deps.CollectionStore.Get(r.Context(), collectionID)
	if err != nil || coll.DeletedAt != nil {
		return false
	}
	if coll.OwnerID == user.UserID {
		return true
	}
	return h.collectionSharePermission(r, collectionID, user) == PermissionEditor
}

// ownsPersonalPrompt reports whether the user owns the given personal prompt.
func (h *Handler) ownsPersonalPrompt(r *http.Request, promptID string, user *User) bool {
	if h.deps.PromptStore == nil {
		return false
	}
	pr, err := h.deps.PromptStore.GetByID(r.Context(), promptID)
	return err == nil && pr != nil && pr.Scope == prompt.ScopePersonal &&
		pr.OwnerEmail != "" && strings.EqualFold(pr.OwnerEmail, user.Email)
}

func (h *Handler) userIsAdmin(user *User) bool {
	return hasAnyRole(user.Roles, h.deps.AdminRoles)
}

// --- small helpers ---

// threadTarget bundles a thread's target discriminator and the 1-of-N object
// ids, so access checks take one value instead of four positional args.
type threadTarget struct {
	kind       string
	asset      string
	collection string
	prompt     string
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
func scopeFromFilter(f ThreadFilter) (string, bool) {
	n := countSet(f.AssetID, f.CollectionID, f.PromptID)
	if f.TargetType == targetTypeStandalone {
		return targetTypeStandalone, n == 0 // standalone must carry no object target
	}
	if n != 1 {
		return "", false
	}
	switch {
	case f.AssetID != "":
		return targetTypeAsset, true
	case f.CollectionID != "":
		return targetTypeCollection, true
	default:
		return targetTypePrompt, true
	}
}

// validThreadTarget reports whether a create request names a valid 1-of-N (or
// standalone) target.
func validThreadTarget(targetType, assetID, collectionID, promptID string) bool {
	n := countSet(assetID, collectionID, promptID)
	switch targetType {
	case targetTypeStandalone:
		return n == 0
	case targetTypeAsset:
		return n == 1 && assetID != ""
	case targetTypeCollection:
		return n == 1 && collectionID != ""
	case targetTypePrompt:
		return n == 1 && promptID != ""
	default:
		return false
	}
}

// validAppendEventType limits client-authored events to conversational kinds;
// status/resolution and knowledge-link events are produced by the system.
func validAppendEventType(eventType string) bool {
	switch eventType {
	case EventTypeComment, EventTypeRating, EventTypeApproval, EventTypeRejection:
		return true
	default:
		return false
	}
}
