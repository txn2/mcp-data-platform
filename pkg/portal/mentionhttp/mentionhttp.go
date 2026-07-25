// Package mentionhttp serves the people surfaces of the portal API: the
// known-users directory behind the share picker (#614), the audience-scoped
// candidate list behind the @-mention picker, and the caller's mentions inbox
// (#627).
//
// It lives beside pkg/portal rather than inside it, like the prompt version
// REST surface, so the portal package stays within its size budget (#594). The
// composition root mounts these routes on the top-level mux wrapped in the
// portal's own authentication middleware and injects the identity accessor, so
// this package never imports pkg/portal.
package mentionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	userdir "github.com/txn2/mcp-data-platform/pkg/user"
)

// Identity is the authenticated portal caller resolved by the injected
// accessor.
type Identity struct {
	UserID  string
	Email   string
	IsAdmin bool
}

// DirectoryReader lists the known-users directory. The picker only reads it,
// so this is the whole surface it takes; pkg/user.Store implements it.
type DirectoryReader interface {
	List(ctx context.Context, filter userdir.Filter) ([]userdir.User, int, error)
}

// AudienceReader answers who may be mentioned on a thread target: the page the
// picker shows, and whether one person belongs to it. pkg/portal/mention.Audience
// implements it.
type AudienceReader interface {
	List(ctx context.Context, t mention.Target, opts mention.ListOptions) ([]mention.Person, error)
	Eligible(ctx context.Context, t mention.Target, emails []string) ([]string, error)
}

// Deps carries the collaborators the handlers need. Each is optional: a
// deployment without a database leaves them nil and the routes they back are
// not registered.
type Deps struct {
	// Directory is the known-users store behind the share picker.
	Directory DirectoryReader
	// Audience answers who may be mentioned on a thread target.
	Audience AudienceReader
	// Threads reads the caller's mentions inbox.
	Threads threads.ThreadStore
	// Caller resolves the authenticated portal user, returning nil when the
	// request carries none.
	Caller func(*http.Request) *Identity
}

// Handler serves the people and mention routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// Query parameters and page bounds. The limit defaults and ceilings match the
// portal's own list endpoints so a client can page these the same way.
const (
	paramQuery      = "q"
	paramLimit      = "limit"
	paramOffset     = "offset"
	paramTargetType = "target_type"
	paramTargetID   = "target_id"

	defaultLimit = 50
)

// Register mounts the routes each wired dependency supports, wrapping every
// one in the portal's authentication middleware. Routes whose dependency is
// missing are left unregistered rather than serving an error, matching how the
// portal itself registers optional surfaces.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h.deps.Caller == nil {
		return
	}
	if h.deps.Directory != nil {
		mux.Handle("GET /api/v1/portal/users", wrap(http.HandlerFunc(h.listDirectoryUsers)))
	}
	if h.deps.Audience != nil {
		mux.Handle("GET /api/v1/portal/mention-candidates", wrap(http.HandlerFunc(h.listMentionCandidates)))
	}
	if h.deps.Threads != nil {
		mux.Handle("GET /api/v1/portal/worklist/mentions", wrap(http.HandlerFunc(h.mentionWorklist)))
	}
}

// directoryUser is the minimal known-user representation exposed to the share
// picker. It deliberately omits bookkeeping fields (source, added_by, seen-at)
// -- the picker only needs to show and resolve a name.
type directoryUser struct {
	Email     string `json:"email" example:"marcus.johnson@example.com"`
	FirstName string `json:"first_name,omitempty" example:"Marcus"`
	LastName  string `json:"last_name,omitempty" example:"Johnson"`
	Confirmed bool   `json:"confirmed" example:"true"`
}

// directoryUsersResponse wraps a page of directory users for the picker.
type directoryUsersResponse struct {
	Users []directoryUser `json:"users"`
	Total int             `json:"total" example:"42"`
}

// listDirectoryUsers handles GET /api/v1/portal/users.
//
// @Summary      List known users for the share picker
// @Description  Returns the known-users directory so a user can pick a teammate to share with. Includes admin-added people who have not logged in yet (confirmed=false). Any authenticated user may call this.
// @Tags         User
// @Produce      json
// @Param        q       query  string   false  "Case-insensitive match on email or name"
// @Param        limit   query  integer  false  "Results per page (default: 50, max: 100)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  directoryUsersResponse
// @Failure      401  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/users [get]
func (h *Handler) listDirectoryUsers(w http.ResponseWriter, r *http.Request) {
	if h.caller(w, r) == nil {
		return
	}
	users, total, err := h.deps.Directory.List(r.Context(), userdir.Filter{
		Query:  r.URL.Query().Get(paramQuery),
		Limit:  intParam(r, paramLimit, defaultLimit),
		Offset: intParam(r, paramOffset, 0),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]directoryUser, 0, len(users))
	for i := range users {
		out = append(out, directoryUser{
			Email:     users[i].Email,
			FirstName: users[i].FirstName,
			LastName:  users[i].LastName,
			Confirmed: users[i].Confirmed,
		})
	}
	writeJSON(w, http.StatusOK, directoryUsersResponse{Users: out, Total: total})
}

// mentionCandidatesResponse wraps the people who may be @-mentioned on a
// target.
type mentionCandidatesResponse struct {
	Candidates []mention.Person `json:"candidates"`
}

// listMentionCandidates handles GET /api/v1/portal/mention-candidates.
//
// @Summary      List who can be @-mentioned on a thread target
// @Description  Returns the people who can open the given asset, collection, prompt, or knowledge page, so the comment composer only offers teammates a mention would actually reach. The caller must be able to open the target themselves.
// @Tags         Feedback
// @Produce      json
// @Param        target_type  query  string   true   "asset, collection, prompt, knowledge_page, or standalone"
// @Param        target_id    query  string   false  "Target id (omitted for standalone)"
// @Param        q            query  string   false  "Case-insensitive match on email or name"
// @Param        limit        query  integer  false  "Results per page (default: 20, max: 100)"
// @Success      200  {object}  mentionCandidatesResponse
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/mention-candidates [get]
func (h *Handler) listMentionCandidates(w http.ResponseWriter, r *http.Request) {
	caller := h.caller(w, r)
	if caller == nil {
		return
	}
	target := mention.Target{
		Type: r.URL.Query().Get(paramTargetType),
		ID:   r.URL.Query().Get(paramTargetID),
	}
	allowed, err := h.callerMaySeeAudience(r, caller, target)
	if err != nil {
		writeAudienceError(w, err, "failed to check access to this item")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "you do not have access to this item")
		return
	}
	people, err := h.deps.Audience.List(r.Context(), target, mention.ListOptions{
		Query:   r.URL.Query().Get(paramQuery),
		Exclude: caller.Email,
		Limit:   intParam(r, paramLimit, 0),
	})
	if err != nil {
		writeAudienceError(w, err, "failed to list mention candidates")
		return
	}
	writeJSON(w, http.StatusOK, mentionCandidatesResponse{Candidates: people})
}

// writeAudienceError maps an audience lookup failure to a status. An
// unrecognized target is the caller's mistake (400); anything else is ours
// (500). Neither is reported as a denial: answering "you do not have access"
// to a lookup that failed tells the caller something untrue and hides a broken
// target id behind a permissions message.
func writeAudienceError(w http.ResponseWriter, err error, detail string) {
	if errors.Is(err, mention.ErrUnknownTarget) {
		writeError(w, http.StatusBadRequest, "unknown target_type")
		return
	}
	writeError(w, http.StatusInternalServerError, detail)
}

// callerMaySeeAudience reports whether the caller may read a target's audience.
// Listing who can see an item is as sensitive as its share list, so for an
// owned item the caller must belong to the audience themselves; an administrator
// always may. Knowledge pages and the standalone channel are open to any
// authenticated user, so their audience -- the known-users directory, already
// readable at /api/v1/portal/users -- needs no membership test, and a caller
// whose own directory row has not been written yet is not refused their own
// picker.
//
// A lookup failure is returned as an error rather than folded into a denial:
// the two have different causes and different answers.
func (h *Handler) callerMaySeeAudience(r *http.Request, caller *Identity, target mention.Target) (bool, error) {
	if caller.IsAdmin || target.Type == mention.TargetKnowledgePage || target.Type == mention.TargetStandalone {
		return true, nil
	}
	member, err := h.deps.Audience.Eligible(r.Context(), target, []string{caller.Email})
	if err != nil {
		return false, fmt.Errorf("resolving audience membership: %w", err)
	}
	return len(member) == 1, nil
}

// paginatedResponse mirrors the portal's list envelope so these routes page
// like the rest of the API.
type paginatedResponse struct {
	Data   any `json:"data"`
	Total  int `json:"total" example:"42"`
	Limit  int `json:"limit" example:"50"`
	Offset int `json:"offset" example:"0"`
}

// mentionWorklist handles GET /api/v1/portal/worklist/mentions.
//
// @Summary      Mentions worklist
// @Description  Feedback threads where a comment @-mentioned the caller, most recently active first.
// @Tags         Feedback
// @Produce      json
// @Param        limit   query  integer  false  "Results per page"
// @Param        offset  query  integer  false  "Offset for pagination"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/worklist/mentions [get]
//
// The filter is the caller's own address, so the list starts self-scoped: a
// mention is recorded only where the write path found that person in the
// target's audience. The rows are then re-checked against present-day access,
// because a mention is a durable record while a share is not: once a share is
// revoked or the item is deleted, its threads must leave the inbox rather than
// keep surfacing the item's title. Rows the caller can no longer reach are
// dropped from the page and from its total.
func (h *Handler) mentionWorklist(w http.ResponseWriter, r *http.Request) {
	caller := h.caller(w, r)
	if caller == nil {
		return
	}
	filter := threads.ThreadFilter{
		MentionedEmail: caller.Email,
		Limit:          intParam(r, paramLimit, 0),
		Offset:         intParam(r, paramOffset, 0),
	}
	found, total, err := h.deps.Threads.ListThreads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load your mentions")
		return
	}
	reachable := h.stillReachable(r, caller, found)
	if reachable == nil {
		reachable = []threads.ThreadWithMeta{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data:   reachable,
		Total:  total - (len(found) - len(reachable)),
		Limit:  filter.EffectiveLimit(),
		Offset: filter.Offset,
	})
}

// stillReachable drops the threads whose target the caller can no longer open.
// Access is resolved once per distinct target, so a page of mentions on one
// asset costs one lookup. Targets with no audience concept (the standalone
// channel) and an admin caller are always reachable; a lookup failure keeps the
// row rather than hiding a thread over a transient database error. Without an
// audience resolver wired there is nothing to re-check against, so the page is
// returned as the store produced it.
func (h *Handler) stillReachable(r *http.Request, caller *Identity, found []threads.ThreadWithMeta) []threads.ThreadWithMeta {
	if h.deps.Audience == nil {
		return found
	}
	reachable := make([]threads.ThreadWithMeta, 0, len(found))
	verdicts := make(map[mention.Target]bool, len(found))
	for _, thread := range found {
		target := mention.Target{Type: thread.TargetType, ID: thread.TargetID()}
		allowed, seen := verdicts[target]
		if !seen {
			var err error
			allowed, err = h.callerMaySeeAudience(r, caller, target)
			if err != nil {
				allowed = true
			}
			verdicts[target] = allowed
		}
		if allowed {
			reachable = append(reachable, thread)
		}
	}
	return reachable
}

// caller resolves the authenticated user, writing a 401 when there is none.
func (h *Handler) caller(w http.ResponseWriter, r *http.Request) *Identity {
	id := h.deps.Caller(r)
	if id == nil || id.Email == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil
	}
	return id
}

// errorBody is the RFC 9457 problem shape the portal API answers errors with.
type errorBody struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}

// intParam reads a non-negative integer query parameter, falling back on an
// absent or malformed value. Page sizes are clamped by the store each route
// calls (mention.ListOptions, threads.ThreadFilter, userdir.Filter), so this
// stays a plain parse.
func intParam(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
