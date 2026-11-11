package sitehttp

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// maxSourceLabel bounds what a release records it came from.
const maxSourceLabel = 512

// publish handles POST /api/v1/portal/sites/{site}/publish.
//
// @Summary      Publish a release
// @Description  Publishes a managed-resource archive or folder, or the objects under a prefix of a bucket on an S3 connection (from_bucket: connection, bucket, prefix; the connection must be one the caller's persona may use and the bucket inside its bucket_prefix, refused here before a job starts), as a new release, in the background. Each object is a file at its path under the prefix, and the release limits apply. The answer is the job to follow; it continues if the caller goes away.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string       true  "Site name or id"
// @Param        body  body      PublishBody  true  "What to publish"
// @Success      202   {object}  site.Job
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/publish [post]
func (a *portalAPI) publish(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in PublishBody
	if !decode(w, r, &in) {
		return
	}
	src := a.d.Runtime.Sources
	if src == nil {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "this deployment has no managed resources to publish from")
		return
	}
	if publishSources(in) != 1 {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "publish names one source: an archive, a folder or from_bucket")
		return
	}
	if len(in.Source) > maxSourceLabel {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "source: too long")
		return
	}
	// Someone who may not publish here learns nothing of a source's
	// connection or bucket: the site is checked before the source is.
	if err := a.svc().MayPublish(r.Context(), c.actor, r.PathValue(paramSite), in.Environment); err != nil {
		fail(w, r, err)
		return
	}
	read, label, err := publishReader(r.Context(), src, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	job, err := a.svc().StartPublish(r.Context(), c.actor, sitesvc.PublishJob{
		Site: r.PathValue(paramSite), Environment: in.Environment, Note: in.Note, Source: cmp.Or(in.Source, label), Read: read,
	})
	a.record(r.Context(), c, sitetool.CmdPublish, map[string]any{paramSite: r.PathValue(paramSite), paramEnv: in.Environment}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// publishSources counts the sources a publish names.
func publishSources(in PublishBody) int {
	n := 0
	for _, set := range []bool{in.Archive != "", in.Folder != "", in.FromBucket != nil} {
		if set {
			n++
		}
	}
	return n
}

// publishReader is what the publish job reads its files with, and what the
// release says it came from. A bucket the caller cannot read is refused here,
// before a job is started for it.
func publishReader(ctx context.Context, src sitetool.Sources, in PublishBody) (read func(context.Context) ([]sitesvc.InputFile, error), source string, err error) {
	switch {
	case in.FromBucket != nil:
		bs, ok := src.(sitetool.BucketSources)
		if !ok {
			return nil, "", &sitesvc.RefusedError{Msg: "this deployment cannot publish from a bucket"}
		}
		from := *in.FromBucket
		if err := bs.CheckBucket(ctx, from); err != nil {
			return nil, "", err //nolint:wrapcheck // the source's own refusal
		}
		return func(ctx context.Context) ([]sitesvc.InputFile, error) {
			return bs.Bucket(ctx, from) //nolint:wrapcheck // said by the job
		}, from.String(), nil
	case in.Archive != "":
		return func(ctx context.Context) ([]sitesvc.InputFile, error) {
			return src.Archive(ctx, in.Archive) //nolint:wrapcheck // said by the job
		}, in.Archive, nil
	}
	return func(ctx context.Context) ([]sitesvc.InputFile, error) {
		return src.Folder(ctx, in.Scope, in.ScopeID, in.Folder) //nolint:wrapcheck // said by the job
	}, in.Folder, nil
}

// copySite handles POST /api/v1/portal/sites/{site}/copy.
//
// @Summary      Copy an existing site
// @Description  Copies a site into a new release, in the background, keeping its URLs. The destination is held to the egress guard, so a private address is refused. Bounds may be lowered, never raised past the deployment's.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string    true  "Site name or id"
// @Param        body  body      CopyBody  true  "What to copy"
// @Success      202   {object}  site.Job
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/copy [post]
func (a *portalAPI) copySite(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in CopyBody
	if !decode(w, r, &in) {
		return
	}
	crawler := a.d.Runtime.Crawler
	if crawler == nil {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "this deployment cannot copy a site")
		return
	}
	limits := a.limits()
	job, err := a.svc().StartCopy(r.Context(), c.actor, sitesvc.CopyJob{
		Site: r.PathValue(paramSite), Environment: in.Environment, Note: in.Note, Source: "crawl " + sitecrawl.SourceOf(in.URL),
		Copy: func(ctx context.Context, progress func(sitesvc.CopyStats) bool) (*sitesvc.CopyResult, error) {
			got, err := crawler.Crawl(ctx, sitecrawl.Request{
				Start: in.URL, FormMap: in.FormMap, PortalURL: a.d.Runtime.PortalURL, Progress: progress,
				MaxPages: lowered(in.MaxPages, limits.CrawlMaxPages), MaxBytes: lowered(in.MaxBytes, limits.CrawlMaxBytes),
			})
			if err != nil {
				return nil, err //nolint:wrapcheck // said by the job
			}
			return &sitesvc.CopyResult{Files: got.Files, Pages: got.Pages, Redirects: got.Redirects, Skipped: got.Skipped}, nil
		},
	})
	a.record(r.Context(), c, sitetool.CmdCrawl, map[string]any{paramSite: r.PathValue(paramSite), paramEnv: in.Environment, "source": sitecrawl.SourceOf(in.URL)}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// lowered is asked when it is a positive bound under limit, otherwise limit.
func lowered[T cmp.Ordered](asked, limit T) T {
	var zero T
	if asked > zero && asked < limit {
		return asked
	}
	return limit
}

// forms handles GET /api/v1/portal/sites/{site}/forms.
//
// @Summary      Forms a copied site can post to
// @Description  The endpoints of this deployment that are forms, for someone who manages the site.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  FormList
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/forms [get]
func (a *portalAPI) forms(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	if err := a.svc().MayPublish(r.Context(), c.actor, r.PathValue(paramSite), ""); err != nil {
		fail(w, r, err)
		return
	}
	out := FormList{Forms: []string{}}
	if a.d.Forms != nil {
		names, err := a.d.Forms(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}
		out.Forms = names
	}
	writeJSON(w, http.StatusOK, out)
}

// jobs handles GET /api/v1/portal/sites/{site}/jobs.
//
// @Summary      A site's running jobs
// @Description  The publishes and copies still running on the site. Someone who does not manage the site sees those to the environments they can open, without their source.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  JobList
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/jobs [get]
func (a *portalAPI) jobs(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	list, err := a.svc().ActiveJobs(r.Context(), r.PathValue(paramSite))
	if err != nil {
		fail(w, r, err)
		return
	}
	list = slices.DeleteFunc(list, func(j site.Job) bool { return !ss.scope.sees(j.Environment) })
	if list == nil {
		list = []site.Job{}
	}
	for i := range list {
		list[i] = ss.scope.jobView(list[i])
	}
	writeJSON(w, http.StatusOK, JobList{Jobs: list})
}

// job handles GET /api/v1/portal/sites/{site}/jobs/{id}.
//
// @Summary      A publish or copy
// @Description  Its phase and counts while it runs; its release, what it could not copy and why, or its error once it ends.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Param        id    path      string  true  "Job id"
// @Success      200   {object}  site.Job
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/jobs/{id} [get]
func (a *portalAPI) job(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	j, err := a.svc().JobOf(r.Context(), r.PathValue(paramSite), r.PathValue("id"))
	if err == nil && !ss.scope.sees(j.Environment) {
		err = site.ErrNoJob
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ss.scope.jobView(*j))
}

// jobView is a job as the caller may see it: its source, like a release's, is
// for the managers.
func (s envScope) jobView(j site.Job) site.Job {
	if !s.manage {
		j.Source = ""
	}
	return j
}

// stopJob handles POST /api/v1/portal/sites/{site}/jobs/{id}/stop.
//
// @Summary      Stop a copy
// @Description  Asks a running copy to stop; it stops at its next address.
// @Tags         Sites
// @Param        site  path  string  true  "Site name or id"
// @Param        id    path  string  true  "Job id"
// @Success      204
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/jobs/{id}/stop [post]
func (a *portalAPI) stopJob(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	if err := a.svc().StopJob(r.Context(), c.actor, r.PathValue(paramSite), r.PathValue("id")); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// promote handles POST /api/v1/portal/sites/{site}/promote.
//
// @Summary      Promote a release
// @Description  Points an environment at another environment's release, or a numbered one. Where the environment requires approval, a non-administrator's promote files a request and is finished by promoting again once it is approved.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string       true  "Site name or id"
// @Param        body  body      PromoteBody  true  "The move"
// @Success      200   {object}  sitesvc.Promoted
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/promote [post]
func (a *portalAPI) promote(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in PromoteBody
	if !decode(w, r, &in) {
		return
	}
	out, err := a.svc().Promote(r.Context(), c.actor, sitesvc.PromoteRequest{
		Site: r.PathValue(paramSite), From: in.From, To: in.To, Release: in.Release, Reason: in.Reason,
	})
	a.record(r.Context(), c, sitetool.CmdPromote, map[string]any{paramSite: r.PathValue(paramSite), "to": in.To}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// rollback handles POST /api/v1/portal/sites/{site}/rollback.
//
// @Summary      Roll back an environment
// @Description  Points an environment at a release it served before, the one before the current when none is named.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string        true  "Site name or id"
// @Param        body  body      RollbackBody  true  "The move"
// @Success      200   {object}  sitesvc.Promoted
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/rollback [post]
func (a *portalAPI) rollback(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in RollbackBody
	if !decode(w, r, &in) {
		return
	}
	out, err := a.svc().Rollback(r.Context(), c.actor, r.PathValue(paramSite), in.Environment, in.Release)
	a.record(r.Context(), c, sitetool.CmdRollback, map[string]any{paramSite: r.PathValue(paramSite), paramEnv: in.Environment}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setEnvironment handles PUT /api/v1/portal/sites/{site}/environments/{name}.
//
// @Summary      Create or change an environment
// @Description  Creates the environment (private unless public is true), or changes whether it is public, whether everyone internal may open it, whether promoting to it needs approval, and how many releases it keeps (keep_releases: a whole number, 0 for every release, or "default"; the owner and administrators only, since a lower limit removes rollback history at the next sweep). Only the owner or an administrator makes an environment public, and where it requires approval a non-administrator's request waits in Approvals (status awaiting_approval) until approved, after which the same request succeeds. dev is always private. Only an administrator takes the approval rule off.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string           true  "Site name or id"
// @Param        name  path      string           true  "Environment"
// @Param        body  body      EnvironmentBody  true  "The change"
// @Success      200   {object}  EnvironmentResponse
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/environments/{name} [put]
func (a *portalAPI) setEnvironment(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in EnvironmentBody
	if !decode(w, r, &in) {
		return
	}
	keep, err := site.ParseKeep(in.KeepReleases)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := a.svc().SetEnvironment(r.Context(), c.actor, r.PathValue(paramSite), r.PathValue("name"), sitesvc.EnvironmentChange{
		RequiresApproval: in.RequiresApproval, Public: in.Public, InternalView: in.InternalView, Reason: in.Reason,
		Keep: keep.Keep, KeepDefault: keep.Default,
	})
	a.record(r.Context(), c, sitetool.CmdEnvironment, map[string]any{paramSite: r.PathValue(paramSite), paramEnv: r.PathValue("name")}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	view, err := a.fullView(r.Context(), c, out.Site)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, EnvironmentResponse{Site: view, Status: out.Status, Approval: out.Approval, Note: out.Note})
}

// setMainEnvironment handles PUT /api/v1/portal/sites/{site}/main-environment.
//
// @Summary      Move the main address
// @Description  Gives the site's main address, <site>.<pages domain>, to one of its environments, or to none with environment "" (it then answers not found). Every environment keeps its <site>--<env> address. The owner and administrators only.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string               true  "Site name or id"
// @Param        body  body      MainEnvironmentBody  true  "The environment"
// @Success      200   {object}  SiteView
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/main-environment [put]
func (a *portalAPI) setMainEnvironment(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in MainEnvironmentBody
	if !decode(w, r, &in) {
		return
	}
	st, err := a.svc().SetMainEnvironment(r.Context(), c.actor, r.PathValue(paramSite), in.Environment)
	a.record(r.Context(), c, sitetool.CmdUpdate, map[string]any{paramSite: r.PathValue(paramSite), "main_environment": in.Environment}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	view, err := a.fullView(r.Context(), c, st)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// fullView is a site as the portal shows it, with its domains read.
func (a *portalAPI) fullView(ctx context.Context, c *caller, st *site.Site) (SiteView, error) {
	domains, err := a.svc().Store.SiteDomains(ctx, st.ID)
	if err != nil {
		return SiteView{}, err //nolint:wrapcheck // answered by fail
	}
	return a.view(ctx, c, st, domains)
}

// addDomain handles POST /api/v1/portal/sites/{site}/domains.
//
// @Summary      Add a domain
// @Description  Maps a hostname to one of the site's environments, creating the environment when it does not exist. It routes as soon as it is added; the deployment's administrator adds the ingress.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string      true  "Site name or id"
// @Param        body  body      DomainBody  true  "The domain"
// @Success      201   {object}  site.Domain
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/domains [post]
func (a *portalAPI) addDomain(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in DomainBody
	if !decode(w, r, &in) {
		return
	}
	d, err := a.svc().AddDomain(r.Context(), c.actor, sitesvc.DomainRequest{
		Hostname: in.Hostname, Site: r.PathValue(paramSite), Environment: in.Environment, RequiresApproval: in.RequiresApproval, Public: in.Public,
	})
	a.record(r.Context(), c, sitetool.CmdAddDomain, map[string]any{paramSite: r.PathValue(paramSite), "hostname": in.Hostname}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// removeDomain handles DELETE /api/v1/portal/sites/{site}/domains/{hostname}.
//
// @Summary      Remove a domain
// @Description  Stops routing a hostname. The environment and its release do not change.
// @Tags         Sites
// @Param        site      path  string  true  "Site name or id"
// @Param        hostname  path  string  true  "Hostname"
// @Success      204
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/domains/{hostname} [delete]
func (a *portalAPI) removeDomain(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	host := r.PathValue("hostname")
	err := a.belongs(r.Context(), r.PathValue(paramSite), host)
	if err == nil {
		err = a.svc().RemoveDomain(r.Context(), c.actor, host)
	}
	a.record(r.Context(), c, sitetool.CmdRemoveDomain, map[string]any{paramSite: r.PathValue(paramSite), "hostname": host}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// belongs refuses a hostname that is not this site's, so a route naming one
// site cannot act on another's domain.
func (a *portalAPI) belongs(ctx context.Context, ref, hostname string) error {
	st, err := a.svc().Find(ctx, ref)
	if err != nil {
		return err //nolint:wrapcheck // answered by fail
	}
	d, err := a.svc().Store.Domain(ctx, site.NormalizeHost(hostname))
	if err != nil {
		return err //nolint:wrapcheck // answered by fail
	}
	if d.SiteID != st.ID {
		return site.ErrNoDomain
	}
	return nil
}

// access handles GET /api/v1/portal/sites/{site}/access.
//
// @Summary      Who has access to a site
// @Description  The owner, the people who can manage the site and the people who can view it. Open to those who manage it.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  AccessList
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/access [get]
func (a *portalAPI) access(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	grants, err := a.svc().Grants(r.Context(), c.actor, r.PathValue(paramSite))
	if err != nil {
		fail(w, r, err)
		return
	}
	st, err := a.svc().Find(r.Context(), r.PathValue(paramSite))
	if err != nil {
		fail(w, r, err)
		return
	}
	personas := []string{}
	if a.d.Personas != nil {
		personas = append(personas, a.d.Personas()...)
	}
	writeJSON(w, http.StatusOK, AccessList{Owner: st.OwnerEmail, Grants: grants, Personas: personas})
}

// grant handles PUT /api/v1/portal/sites/{site}/access. The email is in the
// body, not the path: the API gateway refuses a path holding "@" (#2578).
//
// @Summary      Give someone access
// @Description  Adds a person (email) or a persona, or changes their grant. A view grant names the private environments it opens (none is every one); a persona is always view and is matched against the viewer's persona on every request. Anyone who manages the site shares view access; only its owner and administrators add or change someone who manages it, and that person must be internal.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string     true  "Site name or id"
// @Param        body  body      GrantBody  true  "Who, and at what level"
// @Success      200   {object}  site.Grant
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/access [put]
func (a *portalAPI) grant(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in GrantBody
	if !decode(w, r, &in) {
		return
	}
	g, err := a.svc().Grant(r.Context(), c.actor, r.PathValue(paramSite), sitesvc.GrantRequest{
		Email: in.Email, Persona: in.Persona, Level: in.Level, Environments: in.Environments,
	})
	a.record(r.Context(), c, sitetool.CmdGrant, map[string]any{
		paramSite: r.PathValue(paramSite), paramEmail: in.Email, "persona": in.Persona, "level": in.Level, "environments": in.Environments,
	}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	if in.Notify && g.Email != "" {
		a.share(r.Context(), c, r.PathValue(paramSite), g)
	}
	writeJSON(w, http.StatusOK, g)
}

// share emails someone the site's address. It is a courtesy on top of the
// grant: a failure is logged and the grant stands.
func (a *portalAPI) share(ctx context.Context, c *caller, ref string, g *site.Grant) {
	if a.d.Sharer == nil {
		return
	}
	st, err := a.svc().Find(ctx, ref)
	if err != nil {
		return
	}
	_, err = a.d.Sharer.Notify(ctx, g.Email, notification.CategoryShare, notification.Payload{
		Kind: paramSite, ItemID: st.ID, ItemTitle: st.Name, Actor: c.user.Email,
		Message: "shared a site with you", Link: shareLink(ctx, a.svc(), st, g),
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "sites: emailing a shared site failed", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// revoke handles DELETE /api/v1/portal/sites/{site}/access?email=. The email is
// in the query, not the path: the API gateway refuses a path holding "@" (#2578).
//
// @Summary      Remove someone's access
// @Description  Takes effect on their next request. Anyone who manages the site removes someone who can view it; only its owner and administrators remove someone who manages it.
// @Tags         Sites
// @Param        site   path   string  true  "Site name or id"
// @Param        email    query  string  false  "Their email"
// @Param        persona  query  string  false  "A persona, in place of email"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/access [delete]
func (a *portalAPI) revoke(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	email, persona := r.URL.Query().Get(paramEmail), r.URL.Query().Get("persona")
	if email == "" && persona == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "email or persona is required")
		return
	}
	err := a.svc().Revoke(r.Context(), c.actor, r.PathValue(paramSite), email, persona)
	a.record(r.Context(), c, sitetool.CmdRevoke, map[string]any{paramSite: r.PathValue(paramSite), paramEmail: email, "persona": persona}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shareLink is the address a share email points at: the first private
// environment the grant opens, by its address on the pages domain.
func shareLink(ctx context.Context, svc *sitesvc.Service, st *site.Site, g *site.Grant) string {
	for _, e := range st.Environments {
		if !e.Public && g.Opens(e.Name) {
			return svc.Address(ctx, st, e.Name)
		}
	}
	return svc.OwnAddress(st)
}
