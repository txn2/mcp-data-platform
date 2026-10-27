// Package changesethttp serves the knowledge changesets to the portal (#2337):
// the list, one changeset, and its rollback under
// /api/v1/portal/knowledge/changesets, for a caller who holds apply_knowledge.
//
// The portal's Changesets tab is offered on that capability, not on the admin
// role, and the apply_knowledge tool already lists and rolls back changesets
// for whoever holds it. These routes give the tab the same audience: they run
// the admin API's own changeset handlers, so the two surfaces answer alike,
// with the portal caller named as the one who rolled a changeset back.
//
// It lives beside pkg/portal, like the other portal route packages here, so
// the portal package stays within its size budget.
package changesethttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/agentinstructions"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	knowledge "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// KnowledgeHandler is the knowledge review and changeset handler, nil
// without an insight store: the admin API serves it, and Mount serves its
// changesets to the portal.
func KnowledgeHandler(p *platform.Platform) *admin.KnowledgeHandler {
	if p.KnowledgeInsightStore() == nil {
		return nil
	}
	return admin.NewKnowledgeHandler(
		p.KnowledgeInsightStore(), p.KnowledgeChangesetStore(), p.KnowledgeCatalog(), p.PortalKnowledgePageStore(),
		agentinstructions.New(p.ConfigStore(), p.FileDefaults(), platform.ConfigKeyServerAgentInstructions),
		p.QueryProvider(),
	)
}

// Mount serves kh's changesets to the portal's callers, each behind wrap,
// admitting whoever holds apply_knowledge: the tool resolver names, or an
// administrator. A nil kh mounts nothing.
func Mount(mux *http.ServeMux, wrap func(http.Handler) http.Handler, kh *admin.KnowledgeHandler, resolver portal.PersonaResolver, isAdmin func([]string) bool) {
	if kh == nil {
		return
	}
	cfg := access.Config{AdminRule: isAdmin}
	if resolver != nil {
		cfg.PersonaTools = func(roles []string) []string {
			if info := resolver(roles); info != nil {
				return info.Tools
			}
			return nil
		}
	}
	checker := access.New(cfg)
	Register(mux, wrap, Deps{
		Changesets: kh,
		Caller: func(r *http.Request) *Caller {
			if u := portal.GetUser(r.Context()); u != nil {
				return &Caller{UserID: u.UserID, Email: u.Email, Roles: u.Roles}
			}
			return nil
		},
		CanApply: func(c *Caller) bool {
			return checker.HasApplyKnowledge(&access.User{UserID: c.UserID, Email: c.Email, Roles: c.Roles})
		},
	})
}

// The routes.
const (
	listPath     = "/api/v1/portal/knowledge/changesets"
	itemPath     = listPath + "/{id}"
	rollbackPath = itemPath + "/rollback"
)

// Changesets is the admin API's changeset handler (admin.KnowledgeHandler).
type Changesets interface {
	ListChangesets(w http.ResponseWriter, r *http.Request)
	GetChangeset(w http.ResponseWriter, r *http.Request)
	RollbackChangeset(w http.ResponseWriter, r *http.Request)
}

// Caller is the authenticated portal user.
type Caller struct {
	UserID string
	Email  string
	Roles  []string
}

// Deps carries what the routes need.
type Deps struct {
	Changesets Changesets
	// Caller resolves the request's portal user, nil when it carries none.
	Caller func(*http.Request) *Caller
	// CanApply reports whether the caller holds apply_knowledge
	// (access.Checker.HasApplyKnowledge: the persona's tool, or admin).
	CanApply func(*Caller) bool
}

// Register mounts the routes on mux, each behind wrap (the portal's
// authentication and access gate). Nothing is mounted without a handler.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, d Deps) {
	if d.Changesets == nil || d.Caller == nil || d.CanApply == nil {
		return
	}
	mux.Handle("GET "+listPath, wrap(http.HandlerFunc(d.listChangesets)))
	mux.Handle("GET "+itemPath, wrap(http.HandlerFunc(d.getChangeset)))
	mux.Handle("POST "+rollbackPath, wrap(http.HandlerFunc(d.rollbackChangeset)))
}

// changesetList is a page of changesets, as the admin route answers it.
type changesetList struct { //nolint:unused // swagger model
	Data    []knowledge.Changeset `json:"data"`
	Total   int                   `json:"total" example:"8"`
	Page    int                   `json:"page" example:"1"`
	PerPage int                   `json:"per_page" example:"20"`
}

// serve admits a caller holding apply_knowledge and hands the request to next
// with the caller as the admin handler's user: 401 with no caller, 403 for
// one without the capability.
func (d Deps) serve(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	c := d.Caller(r)
	if c == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if !d.CanApply(c) {
		httpjson.WriteError(w, http.StatusForbidden, "changesets require apply_knowledge access")
		return
	}
	u := &admin.User{UserID: c.UserID, Email: c.Email, Roles: c.Roles}
	next(w, r.WithContext(admin.ContextWithUser(r.Context(), u)))
}

// listChangesets handles GET /api/v1/portal/knowledge/changesets.
//
// @Summary      List changesets
// @Description  Returns paginated knowledge changesets with optional filtering, for a caller who holds apply_knowledge (#2337). The same list the admin route answers.
// @Tags         Knowledge
// @Produce      json
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        applied_by   query  string  false  "Filter by applier"
// @Param        rolled_back  query  boolean false  "Filter by rollback state"
// @Param        since        query  string  false  "Changesets after this time (RFC 3339)"
// @Param        until        query  string  false  "Changesets before this time (RFC 3339)"
// @Param        page         query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page     query  integer false  "Results per page (default: 20)"
// @Success      200  {object}  changesetList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/changesets [get]
func (d Deps) listChangesets(w http.ResponseWriter, r *http.Request) {
	d.serve(w, r, d.Changesets.ListChangesets)
}

// getChangeset handles GET /api/v1/portal/knowledge/changesets/{id}.
//
// @Summary      Get changeset
// @Description  Returns one knowledge changeset, for a caller who holds apply_knowledge (#2337).
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Changeset ID"
// @Success      200  {object}  knowledge.Changeset
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/changesets/{id} [get]
func (d Deps) getChangeset(w http.ResponseWriter, r *http.Request) {
	d.serve(w, r, d.Changesets.GetChangeset)
}

// rollbackChangeset handles POST /api/v1/portal/knowledge/changesets/{id}/rollback.
//
// @Summary      Rollback changeset
// @Description  Reverts what a changeset wrote, as the admin route does, for a caller who holds apply_knowledge (#2337); the caller is recorded as the one who rolled it back. Refused (409) when already rolled back or when a newer changeset has since touched the same aspect, and (422) when the changeset contains change types whose prior state was not captured.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Changeset ID"
// @Success      200  {object}  knowledge.RollbackResult
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/changesets/{id}/rollback [post]
func (d Deps) rollbackChangeset(w http.ResponseWriter, r *http.Request) {
	d.serve(w, r, d.Changesets.RollbackChangeset)
}
