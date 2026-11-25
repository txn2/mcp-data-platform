// Package typecheckhttp serves a site's type checks in the portal (#2793): the
// newest check of each release, one release's check, and checking a release
// again.
package typecheckhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetypecheck"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// pathSite is the path naming one site, under which the routes are served.
const pathSite = "/api/v1/portal/sites/{site}"

// maxBody bounds a request body.
const maxBody = 4 << 10

// noChecks is the refusal of a deployment that runs no type checks.
const noChecks = "this deployment runs no type checks: it has no bucket to keep the compiler in, or sites.typecheck.enabled is false"

// Queue is the type checks as the routes read and queue them; the
// sitetypecheck Queue is one.
type Queue interface {
	sitetypecheck.Enqueuer
	Summaries(ctx context.Context, siteID string) (map[int]sitetypecheck.Summary, error)
	Latest(ctx context.Context, siteID string, release int) (*sitetypecheck.Check, error)
}

// Caller is who asks of a site's type checks, and the site.
type Caller struct {
	SiteID string
	Actor  sitesvc.Actor
}

// Deps is what the routes read.
type Deps struct {
	// Site is the site a request names and who asks, for a caller who may
	// see the site; on a refusal it has answered the request itself.
	Site func(w http.ResponseWriter, r *http.Request) (Caller, *http.Request, bool)
	// Queue holds the checks; nil where the deployment runs none.
	Queue Queue
	// Releases reads a release and authorizes a re-check.
	Releases sitetypecheck.Releases
	// Fail answers an error from the site service.
	Fail func(w http.ResponseWriter, r *http.Request, err error)
}

type routes struct{ d Deps }

// Register mounts the routes.
func Register(mux *http.ServeMux, d Deps) {
	// A nil *Queue in the interface would read as a queue and panic on use.
	if q, ok := d.Queue.(*sitetypecheck.Queue); ok && q == nil {
		d.Queue = nil
	}
	h := routes{d: d}
	mux.HandleFunc("GET "+pathSite+"/typechecks", h.typechecks)
	mux.HandleFunc("GET "+pathSite+"/releases/{number}/typecheck", h.typecheck)
	mux.HandleFunc("POST "+pathSite+"/releases/{number}/typecheck", h.retypecheck)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, v)
}

// release is the path's release number, answering a malformed one itself.
func release(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "number: a release number")
		return 0, false
	}
	return n, true
}

// typechecks handles GET /api/v1/portal/sites/{site}/typechecks.
//
// @Summary      The newest type check of each release
// @Description  For a site whose _build.json asks for type checks (#2793): the newest check of each release, by release number, with its status (queued, running, clean, errors, over_limit, unsupported or failed) and how many errors it found. A release never checked is absent.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  TypecheckSummaries
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/typechecks [get]
func (h routes) typechecks(w http.ResponseWriter, r *http.Request) {
	c, r, ok := h.d.Site(w, r)
	if !ok {
		return
	}
	out := TypecheckSummaries{Releases: map[string]sitetypecheck.Summary{}}
	if h.d.Queue != nil {
		byNumber, err := h.d.Queue.Summaries(r.Context(), c.SiteID)
		if err != nil {
			h.d.Fail(w, r, err)
			return
		}
		for n, s := range byNumber {
			out.Releases[strconv.Itoa(n)] = s
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// typecheck handles GET /api/v1/portal/sites/{site}/releases/{number}/typecheck.
//
// @Summary      A release's type check
// @Description  The newest type check of a release (#2793): its status, each error's file, line, column, code and message, the packages the code imports that the lockfile does not lock, and how long it took; a queued check says how many run before it. check is null when none has run.
// @Tags         Sites
// @Produce      json
// @Param        site    path      string  true  "Site name or id"
// @Param        number  path      int     true  "Release number"
// @Success      200     {object}  TypecheckView
// @Failure      404     {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/releases/{number}/typecheck [get]
func (h routes) typecheck(w http.ResponseWriter, r *http.Request) {
	c, r, ok := h.d.Site(w, r)
	if !ok {
		return
	}
	number, ok := release(w, r)
	if !ok {
		return
	}
	view := TypecheckView{Release: number}
	if h.d.Queue != nil {
		var err error
		if view.Check, err = h.d.Queue.Latest(r.Context(), c.SiteID, number); err != nil {
			h.d.Fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// retypecheck handles POST /api/v1/portal/sites/{site}/releases/{number}/typecheck.
//
// @Summary      Check a release's types again
// @Description  Queues the release's type check again, for someone who may publish to the site (#2793). The release's _build.json must ask for one; a deployment that runs no checks refuses.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site    path      string         true   "Site name or id"
// @Param        number  path      int            true   "Release number"
// @Param        body    body      TypecheckBody  false  "The environment whose incremental state the check uses"
// @Success      202     {object}  TypecheckView
// @Failure      422     {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/releases/{number}/typecheck [post]
func (h routes) retypecheck(w http.ResponseWriter, r *http.Request) {
	c, r, ok := h.d.Site(w, r)
	if !ok {
		return
	}
	number, ok := release(w, r)
	if !ok {
		return
	}
	var in TypecheckBody
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "the request body is not valid JSON")
			return
		}
	}
	if h.d.Queue == nil {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, noChecks)
		return
	}
	n, _, err := sitetypecheck.Recheck(r.Context(), h.d.Queue, h.d.Releases, sitetypecheck.RecheckRequest{
		Actor: c.Actor, Site: c.SiteID, Environment: in.Environment, Release: number,
	})
	if err != nil {
		h.d.Fail(w, r, err)
		return
	}
	view := TypecheckView{Release: n}
	view.Check, _ = h.d.Queue.Latest(r.Context(), c.SiteID, n)
	writeJSON(w, http.StatusAccepted, view)
}
