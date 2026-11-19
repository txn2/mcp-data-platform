package assettags

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The routes.
const (
	portalPath = "/api/v1/portal/tags"
	adminPath  = "/api/v1/admin/tags"
	renamePath = "/api/v1/admin/tags/rename"
	mergePath  = "/api/v1/admin/tags/merge"
)

// The audit record's tool names for the two administrator actions.
const (
	ToolRename = "admin_tags_rename"
	ToolMerge  = "admin_tags_merge"
)

// maxBody bounds a rename or merge request.
const maxBody = 64 << 10

// Refusals the routes answer with.
const (
	errAuthRequired = "authentication required"
	errBadBody      = "invalid request body"
	errStore        = "the tags could not be read"
	errRewrite      = "the tags could not be rewritten"
)

// Lister is what the portal route reads.
type Lister interface {
	List(ctx context.Context, q Query) (Page, error)
}

// Rewriter is what the administrator routes read and write.
type Rewriter interface {
	Lister
	Apply(ctx context.Context, r Retag, now time.Time) (int64, error)
}

// MountPortal returns the hook pkg/portal registers the portal tag route
// through: GET /api/v1/portal/tags, the tags on the assets the caller can open.
func MountPortal(store Lister) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, _ *access.Checker) {
		mux.HandleFunc("GET "+portalPath, portalList(store))
	}
}

// portalList handles GET /api/v1/portal/tags.
//
// @Summary      List the tags on the assets you can open
// @Description  The tags on every asset the caller can open (their own and those shared with them), compared without case, with how many of those assets carry each, most carried first. prefix narrows to tags beginning with it, without case. The tag input suggests from this; a tag only on assets the caller cannot open is never listed or counted (#2508).
// @Tags         Assets
// @Produce      json
// @Param        prefix  query  string   false  "Tags beginning with this, without case"
// @Param        limit   query  integer  false  "Tags per page (default 50, max 500)"
// @Param        offset  query  integer  false  "Offset into the tags"
// @Success      200  {object}  Page
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/tags [get]
func portalList(store Lister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := access.GetUser(r.Context())
		if user == nil {
			httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
			return
		}
		q := pageQuery(r, "prefix")
		q.Reach = portaldomain.NewReach(user.UserID, user.Email, portaldomain.CallerPersona(r.Context()), portaldomain.ScopeAll)
		page, err := store.List(r.Context(), q)
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, errStore)
			return
		}
		httpjson.WriteJSON(w, http.StatusOK, page)
	}
}

func pageQuery(r *http.Request, prefixParam string) Query {
	v := r.URL.Query()
	limit, _ := strconv.Atoi(v.Get("limit"))
	offset, _ := strconv.Atoi(v.Get("offset"))
	return Query{Prefix: v.Get(prefixParam), Limit: limit, Offset: offset}
}

// AdminConfig is what the administrator routes need.
type AdminConfig struct {
	Store Rewriter
	// Author is the acting administrator's address.
	Author func(*http.Request) string
	// Audit records each rename and merge; nil records nothing.
	Audit middleware.AuditLogger
	// Now is the clock a rewrite is stamped with; nil is time.Now.
	Now func() time.Time
}

type admin struct{ cfg AdminConfig }

// RegisterAdmin mounts the administrator's tag routes behind wrap, the admin
// API's authentication.
func RegisterAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg AdminConfig) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &admin{cfg: cfg}
	mux.Handle("GET "+adminPath, wrap(http.HandlerFunc(a.list)))
	mux.Handle("POST "+renamePath, wrap(http.HandlerFunc(a.rename)))
	mux.Handle("POST "+mergePath, wrap(http.HandlerFunc(a.merge)))
}

// list handles GET /api/v1/admin/tags.
//
// @Summary      List every tag
// @Description  Every tag on every live asset in the deployment, compared without case, with how many assets carry it and the spellings it covers, most carried first (#2508). q narrows to tags beginning with it.
// @Tags         Assets
// @Produce      json
// @Param        q       query  string   false  "Tags beginning with this, without case"
// @Param        limit   query  integer  false  "Tags per page (default 50, max 500)"
// @Param        offset  query  integer  false  "Offset into the tags"
// @Success      200  {object}  Page
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tags [get]
func (a *admin) list(w http.ResponseWriter, r *http.Request) {
	page, err := a.cfg.Store.List(r.Context(), pageQuery(r, "q"))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, page)
}

// rename handles POST /api/v1/admin/tags/rename.
//
// @Summary      Rename a tag
// @Description  Rewrites one tag on every asset that carries it, in any spelling, to the new name, keeping each asset's tag order and carrying the new name once (#2508). moved is how many assets changed. Recorded in the audit log.
// @Tags         Assets
// @Accept       json
// @Produce      json
// @Param        body  body  RenameRequest  true  "The tag and its new name"
// @Success      200  {object}  RetagResult
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tags/rename [post]
func (a *admin) rename(w http.ResponseWriter, r *http.Request) {
	var req RenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	a.retag(w, r, ToolRename, Retag{Sources: []string{req.From}, Target: req.To})
}

// merge handles POST /api/v1/admin/tags/merge.
//
// @Summary      Merge tags
// @Description  Rewrites every tag named in from, in any spelling, and every other spelling of into, to into, on every asset that carries one, keeping each asset's tag order and carrying into once (#2508). moved is how many assets changed. Recorded in the audit log.
// @Tags         Assets
// @Accept       json
// @Produce      json
// @Param        body  body  MergeRequest  true  "The tags and the tag they merge into"
// @Success      200  {object}  RetagResult
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tags/merge [post]
func (a *admin) merge(w http.ResponseWriter, r *http.Request) {
	var req MergeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	a.retag(w, r, ToolMerge, Retag{Sources: req.From, Target: req.Into})
}

func (a *admin) retag(w http.ResponseWriter, r *http.Request, tool string, req Retag) {
	req, err := req.Normalize()
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	moved, err := a.cfg.Store.Apply(r.Context(), req, a.cfg.Now())
	a.record(r, tool, req, moved, err)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errRewrite)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, RetagResult{Tag: req.Target, Moved: moved})
}

// record audits one rename or merge. Best-effort: a failed write is warned
// and the change stands.
func (a *admin) record(r *http.Request, tool string, req Retag, moved int64, opErr error) {
	if a.cfg.Audit == nil {
		return
	}
	actor := ""
	if a.cfg.Author != nil {
		actor = a.cfg.Author(r)
	}
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: a.cfg.Now().UTC(), ToolName: tool, ToolkitKind: "portal",
		Source: "admin", Transport: "http", EventKind: string(audit.EventTypeAdmin),
		Authorized: true, Success: opErr == nil, UserID: actor, UserEmail: actor,
		Parameters: map[string]any{"from": req.Sources, "into": req.Target, "moved": moved},
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := a.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("tag change audit failed", "tool", tool, "error", logsan.SanitizeForLog(err.Error())) // #nosec G706 -- structured slog call; error sanitized
	}
}
