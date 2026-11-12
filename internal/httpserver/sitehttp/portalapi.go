package sitehttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

const (
	portalSites  = "/api/v1/portal/sites"
	paramSite    = "site"
	paramEmail   = "email"
	paramEnv     = "environment"
	maxBody      = 64 << 10
	pathSite     = portalSites + "/{site}"
	claimUnverif = "email_verified"
)

// Sharer emails someone that a site was shared with them; nil sends nothing.
type Sharer interface {
	Notify(ctx context.Context, recipient, category string, p notification.Payload) (bool, error)
}

// PortalDeps is what the portal routes act through.
type PortalDeps struct {
	Runtime *sitewire.Runtime
	// Persona is the caller's resolved persona, nil for none.
	Persona func(roles []string) *portal.PersonaInfo
	IsAdmin func(roles []string) bool
	// Audit records each write; nil records none.
	Audit audit.Logger
	// Sharer emails a person a site was shared with; nil sends nothing.
	Sharer Sharer
	// Forms lists the endpoints that are forms; nil offers none.
	Forms func(ctx context.Context) ([]string, error)
	// Personas lists every persona an environment can be shared with; nil
	// offers none.
	Personas func() []string
}

type portalAPI struct{ d PortalDeps }

// PortalRegistrar registers the portal's Sites routes; nil where the deployment
// has no sites.
func PortalRegistrar(d PortalDeps) func(*http.ServeMux) {
	if d.Runtime == nil {
		return nil
	}
	return func(mux *http.ServeMux) { (&portalAPI{d: d}).register(mux) }
}

func (a *portalAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/application-addresses/{name}", a.applicationAddresses)
	mux.HandleFunc("GET "+portalSites, a.list)
	mux.HandleFunc("POST "+portalSites, a.create)
	mux.HandleFunc("GET "+pathSite, a.get)
	mux.HandleFunc("PATCH "+pathSite, a.update)
	mux.HandleFunc("DELETE "+pathSite, a.remove)
	mux.HandleFunc("GET "+pathSite+"/releases", a.releases)
	mux.HandleFunc("GET "+pathSite+"/releases/{number}", a.release)
	mux.HandleFunc("GET "+pathSite+"/compare", a.compare)
	mux.HandleFunc("GET "+pathSite+"/deployments", a.deployments)
	mux.HandleFunc("POST "+pathSite+"/publish", a.publish)
	mux.HandleFunc("POST "+pathSite+"/copy", a.copySite)
	mux.HandleFunc("GET "+pathSite+"/forms", a.forms)
	mux.HandleFunc("GET "+pathSite+"/jobs", a.jobs)
	mux.HandleFunc("GET "+pathSite+"/jobs/{id}", a.job)
	mux.HandleFunc("POST "+pathSite+"/jobs/{id}/stop", a.stopJob)
	mux.HandleFunc("POST "+pathSite+"/promote", a.promote)
	mux.HandleFunc("POST "+pathSite+"/rollback", a.rollback)
	mux.HandleFunc("PUT "+pathSite+"/environments/{name}", a.setEnvironment)
	mux.HandleFunc("PUT "+pathSite+"/main-environment", a.setMainEnvironment)
	mux.HandleFunc("POST "+pathSite+"/domains", a.addDomain)
	mux.HandleFunc("PUT "+pathSite+"/domains/{hostname}", a.setDomainAccess)
	mux.HandleFunc("DELETE "+pathSite+"/domains/{hostname}", a.removeDomain)
	mux.HandleFunc("GET "+pathSite+"/access", a.access)
	mux.HandleFunc("PUT "+pathSite+"/access", a.grant)
	mux.HandleFunc("DELETE "+pathSite+"/access", a.revoke)
	a.registerFiles(mux)
}

// caller is who a portal request is from.
type caller struct {
	user  *portal.User
	info  *portal.PersonaInfo
	actor sitesvc.Actor
}

// who resolves the caller, carrying the identity a background job and the
// managed-resource reader act under.
func (a *portalAPI) who(w http.ResponseWriter, r *http.Request) (*caller, *http.Request, bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return nil, nil, false
	}
	c := &caller{user: user}
	if a.d.Persona != nil {
		c.info = a.d.Persona(user.Roles)
	}
	admin := a.d.IsAdmin != nil && a.d.IsAdmin(user.Roles)
	verified, stated := user.Claims[claimUnverif].(bool)
	c.actor = sitesvc.Actor{Email: user.Email, Persona: c.personaName(), Admin: admin, EmailUnverified: stated && !verified}
	pc := middleware.NewPlatformContext("")
	pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin = user.UserID, user.Email, c.personaName(), user.Roles, admin
	return c, r.WithContext(middleware.WithPlatformContext(r.Context(), pc)), true
}

func (c *caller) personaName() string {
	if c.info == nil {
		return ""
	}
	return c.info.Name
}

func (a *portalAPI) svc() *sitesvc.Service { return a.d.Runtime.Service }

// fail answers an error from the service: what the caller can act on in its
// own words, and anything else in general terms.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	if refused, ok := errors.AsType[*sitesvc.RefusedError](err); ok {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, refused.Msg)
		return
	}
	for _, m := range errorStatuses {
		if errors.Is(err, m.err) {
			httpjson.WriteError(w, m.status, m.msg)
			return
		}
	}
	slog.ErrorContext(r.Context(), "sites: a portal request failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "this could not be done; try again")
}

var errorStatuses = []struct {
	err    error
	status int
	msg    string
}{
	{site.ErrNotAllowed, http.StatusForbidden, "you do not have permission to do this on this site"},
	{site.ErrNotFound, http.StatusNotFound, "no such site"},
	{site.ErrNoEnvironment, http.StatusNotFound, "the site has no such environment"},
	{site.ErrNoRelease, http.StatusNotFound, "the site has no such release"},
	{site.ErrNoDomain, http.StatusNotFound, "no such domain"},
	{site.ErrNoGrant, http.StatusNotFound, "that person has no access to this site"},
	{site.ErrNoJob, http.StatusNotFound, "no such job"},
	{application.ErrNotFound, http.StatusNotFound, "no such application"},
	{site.ErrNameTaken, http.StatusConflict, "a site with that name exists"},
	{site.ErrDomainTaken, http.StatusConflict, "that domain is already in use"},
	{site.ErrStale, http.StatusConflict, "someone else moved this environment first; look again and retry"},
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not valid JSON")
		return false
	}
	return true
}

// record audits one write as the manage_site call it stands for, made from the
// portal. A failure to record is logged, never the request's.
func (a *portalAPI) record(ctx context.Context, c *caller, command string, params map[string]any, opErr error) {
	if a.d.Audit == nil {
		return
	}
	ev := audit.NewEvent(sitetool.ToolName)
	ev.UserID, ev.UserEmail, ev.Persona = c.user.UserID, c.user.Email, c.personaName()
	ev.ToolkitKind = paramSite
	params["command"] = command
	ev.Parameters = params
	ev.Source, ev.Transport = "portal", "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized, ev.Success = true, opErr == nil
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := a.d.Audit.Log(ctx, *ev); err != nil {
		slog.WarnContext(ctx, "sites: audit log failed", "error", logsan.SanitizeForLog(err.Error()), "command", command)
	}
}

func (a *portalAPI) limits() SiteLimits {
	s := a.d.Runtime.Live.Get().Settings()
	return SiteLimits{
		MaxFiles: s.MaxFiles, MaxFileBytes: s.MaxFileBytes, MaxReleaseBytes: s.MaxReleaseBytes,
		CrawlMaxPages: s.CrawlMaxPages, CrawlMaxBytes: s.CrawlMaxBytes,
	}
}

func (a *portalAPI) view(ctx context.Context, c *caller, st *site.Site, domains []site.Domain) (SiteView, error) {
	perms, err := a.svc().PermissionsOf(ctx, c.actor, st)
	if err != nil {
		return SiteView{}, err //nolint:wrapcheck // answered by fail
	}
	if !perms.Manage {
		// Someone who does not manage the site sees the environments they
		// can open and no other: which private environments exist, and who
		// they are shared with, is the managers' to know.
		trimmed := *st
		if trimmed.Environments, err = a.svc().OpenableEnvironments(ctx, c.actor, st); err != nil {
			return SiteView{}, err //nolint:wrapcheck // answered by fail
		}
		st = &trimmed
		domains = slices.DeleteFunc(slices.Clone(domains), func(d site.Domain) bool { return trimmed.Environment(d.Environment) == nil })
	}
	at := a.svc().Addressing()
	return SiteView{
		Site: *st, Address: at.OwnAddress(st), Addresses: at.Addresses(st, domains), PagesAddresses: at.PagesAddresses(st),
		AddressNote: at.AddressNote(st), Domains: domains, Permissions: perms,
	}, nil
}

// envScope is what a caller may know of a site's environments: every one for
// someone who manages it, and otherwise the ones they can open, the rule view
// applies to the site itself. The release, history and job routes answer
// through it, so a private environment is named to its managers only.
type envScope struct {
	manage bool
	open   map[string]bool
}

func (a *portalAPI) scopeOf(ctx context.Context, c *caller, st *site.Site) (envScope, error) {
	perms, err := a.svc().PermissionsOf(ctx, c.actor, st)
	if err != nil {
		return envScope{}, err //nolint:wrapcheck // answered by fail
	}
	if perms.Manage {
		return envScope{manage: true}, nil
	}
	envs, err := a.svc().OpenableEnvironments(ctx, c.actor, st)
	if err != nil {
		return envScope{}, err //nolint:wrapcheck // answered by fail
	}
	open := make(map[string]bool, len(envs))
	for _, e := range envs {
		open[e.Name] = true
	}
	return envScope{open: open}, nil
}

func (s envScope) sees(env string) bool { return s.manage || s.open[env] }

// names keeps what the caller may know of: for someone who does not manage
// the site, the environments they can open, and none of the other reasons
// (an application's pin, a pending approval), which are the managers' too.
func (s envScope) names(in []string) []string {
	out := []string{}
	for _, n := range in {
		if s.sees(n) {
			out = append(out, n)
		}
	}
	return out
}

// releaseView is a release as the caller may see it: where it came from (a
// connection and bucket, an archive, a crawled origin) is for the managers.
func (s envScope) releaseView(st *site.Site, rel site.Release, kept []string) ReleaseView {
	if kept == nil || rel.Pruned {
		kept = []string{}
	}
	if !s.manage {
		rel.Source = ""
	}
	return ReleaseView{Release: rel, ServedIn: s.names(servedIn(st, rel.Number)), KeptBy: s.names(kept)}
}

// scopedSite is a site a request names and what the caller may know of its
// environments.
type scopedSite struct {
	*site.Site
	scope envScope
}

// siteAndScope resolves the site a request names and what the caller may know
// of its environments, answering the request itself on a failure.
func (a *portalAPI) siteAndScope(w http.ResponseWriter, r *http.Request) (*scopedSite, *http.Request, bool) {
	c, r, ok := a.who(w, r)
	if !ok {
		return nil, nil, false
	}
	st, err := a.svc().Find(r.Context(), r.PathValue(paramSite))
	if err != nil {
		fail(w, r, err)
		return nil, nil, false
	}
	scope, err := a.scopeOf(r.Context(), c, st)
	if err != nil {
		fail(w, r, err)
		return nil, nil, false
	}
	return &scopedSite{Site: st, scope: scope}, r, true
}

// canCreate reports whether the caller's persona may create a site: it holds
// manage_site, the tool a site is otherwise created with.
func (c *caller) canCreate() bool {
	return c.actor.Admin || (c.info != nil && slices.Contains(c.info.Tools, sitetool.ToolName))
}

// list handles GET /api/v1/portal/sites.
//
// @Summary      List sites
// @Description  Every site with its environments, its own address and its domains, what the caller may do to each, the pages domain and the limits a release is held to.
// @Tags         Sites
// @Produce      json
// @Success      200  {object}  SiteList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites [get]
func (a *portalAPI) list(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	sites, err := a.svc().Store.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	domains, err := a.svc().Store.Domains(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	bySite := map[string][]site.Domain{}
	for _, d := range domains {
		bySite[d.SiteID] = append(bySite[d.SiteID], d)
	}
	out := SiteList{
		Sites: make([]SiteView, 0, len(sites)), Total: len(sites), PagesDomain: a.d.Runtime.Live.Get().PagesDomain,
		Limits: a.limits(), CanCreate: c.canCreate(), Admin: c.actor.Admin,
	}
	for i := range sites {
		v, err := a.view(r.Context(), c, &sites[i], nonNil(bySite[sites[i].ID]))
		if err != nil {
			fail(w, r, err)
			return
		}
		out.Sites = append(out.Sites, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func nonNil(in []site.Domain) []site.Domain {
	if in == nil {
		return []site.Domain{}
	}
	return in
}

// create handles POST /api/v1/portal/sites.
//
// @Summary      Create a site
// @Description  Creates a site with its default environment. The caller, whose persona must hold manage_site, is its owner.
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        body  body      SiteRequest  true  "The site"
// @Success      201   {object}  SiteView
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      409   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites [post]
func (a *portalAPI) create(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	if !c.canCreate() {
		httpjson.WriteError(w, http.StatusForbidden, "your persona does not include creating sites")
		return
	}
	var in SiteRequest
	if !decode(w, r, &in) {
		return
	}
	st, err := a.svc().Create(r.Context(), c.actor, in.Name, in.Description)
	a.record(r.Context(), c, sitetool.CmdCreate, map[string]any{"name": in.Name}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	v, err := a.view(r.Context(), c, st, []site.Domain{})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// get handles GET /api/v1/portal/sites/{site}.
//
// @Summary      Read a site
// @Description  One site with its environments, addresses and what the caller may do to it.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  SiteView
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site} [get]
func (a *portalAPI) get(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	st, err := a.svc().Find(r.Context(), r.PathValue(paramSite))
	if err != nil {
		fail(w, r, err)
		return
	}
	domains, err := a.svc().Store.SiteDomains(r.Context(), st.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	v, err := a.view(r.Context(), c, st, domains)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// update handles PATCH /api/v1/portal/sites/{site}.
//
// @Summary      Change a site
// @Description  Changes the description and the owner (owner and administrators), and the resource folders the site serves as media at URL prefixes (media: the whole list, [] for none; anyone who manages the site, for folders they can read; a mounted folder's files are public on a public environment and its HTML, SVG, JavaScript and XML are sent as downloads).
// @Tags         Sites
// @Accept       json
// @Produce      json
// @Param        site  path      string      true  "Site name or id"
// @Param        body  body      SiteUpdate  true  "The change"
// @Success      200   {object}  site.Site
// @Failure      403   {object}  httpjson.ProblemDetail
// @Failure      404   {object}  httpjson.ProblemDetail
// @Failure      422   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site} [patch]
func (a *portalAPI) update(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	var in SiteUpdate
	if !decode(w, r, &in) {
		return
	}
	st, err := a.svc().Update(r.Context(), c.actor, r.PathValue(paramSite), sitesvc.UpdateRequest{
		Description: in.Description, Owner: in.Owner, Media: in.Media,
	})
	a.record(r.Context(), c, sitetool.CmdUpdate, map[string]any{paramSite: r.PathValue(paramSite)}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// remove handles DELETE /api/v1/portal/sites/{site}.
//
// @Summary      Delete a site
// @Description  Deletes a site, its releases and its domains. Administrators only.
// @Tags         Sites
// @Param        site  path  string  true  "Site name or id"
// @Success      204
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site} [delete]
func (a *portalAPI) remove(w http.ResponseWriter, r *http.Request) {
	c, r, ok := a.who(w, r)
	if !ok {
		return
	}
	err := a.svc().Delete(r.Context(), c.actor, r.PathValue(paramSite))
	a.record(r.Context(), c, sitetool.CmdDelete, map[string]any{paramSite: r.PathValue(paramSite)}, err)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// releases handles GET /api/v1/portal/sites/{site}/releases.
//
// @Summary      List a site's releases
// @Description  Newest first, each with the environments serving it and what keeps it from the next cleanup. Someone who does not manage the site sees only the environments they can open, and no release's source.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true  "Site name or id"
// @Success      200   {object}  ReleaseList
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/releases [get]
func (a *portalAPI) releases(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	rels, err := a.svc().Store.Releases(r.Context(), ss.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	kept, err := a.keptBy(r, ss.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	out := ReleaseList{Releases: make([]ReleaseView, 0, len(rels))}
	for _, rel := range rels {
		out.Releases = append(out.Releases, ss.scope.releaseView(ss.Site, rel, kept[rel.Number]))
	}
	writeJSON(w, http.StatusOK, out)
}

// keptBy is what keeps each of the site's releases; none where the deployment
// runs without the store (a test of the routes alone).
func (a *portalAPI) keptBy(r *http.Request, siteID string) (map[int][]string, error) {
	if a.d.Runtime.Store == nil {
		return map[int][]string{}, nil
	}
	return a.d.Runtime.Store.KeptBy(r.Context(), siteID) //nolint:wrapcheck // the store's own sentence
}

func servedIn(st *site.Site, number int) []string {
	out := []string{}
	for _, e := range st.Environments {
		if e.Release == number {
			out = append(out, e.Name)
		}
	}
	return out
}

// release handles GET /api/v1/portal/sites/{site}/releases/{number}.
//
// @Summary      Read a release
// @Description  The release's file list and redirect map. Its files are served only on the site's own origins, never by this route. Someone who does not manage the site sees only the environments they can open, and not its source.
// @Tags         Sites
// @Produce      json
// @Param        site    path      string  true  "Site name or id"
// @Param        number  path      int     true  "Release number"
// @Success      200     {object}  ReleaseDetail
// @Failure      404     {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/releases/{number} [get]
func (a *portalAPI) release(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "number: a release number")
		return
	}
	rel, err := a.svc().Store.Release(r.Context(), ss.ID, number)
	if err != nil {
		fail(w, r, err)
		return
	}
	files := []site.File{}
	if !rel.Pruned {
		// A pruned release's own rows are gone, and resolving its chain would
		// read a kept ancestor's files as if they were its own.
		if files, err = a.svc().Store.Files(r.Context(), rel.ID); err != nil {
			fail(w, r, err)
			return
		}
	}
	kept, err := a.keptBy(r, ss.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ReleaseDetail{ReleaseView: ss.scope.releaseView(ss.Site, *rel, kept[number]), Files: files})
}

// compare handles GET /api/v1/portal/sites/{site}/compare.
//
// @Summary      Compare two releases
// @Description  The files changed, added and removed and the redirects added and removed by moving from one release to another, read from each release's file digests.
// @Tags         Sites
// @Produce      json
// @Param        site  path      string  true   "Site name or id"
// @Param        from  query     int     false  "The release served now; 0 or absent for none"
// @Param        to    query     int     true   "The release that would be served"
// @Success      200   {object}  sitesvc.Comparison
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/compare [get]
func (a *portalAPI) compare(w http.ResponseWriter, r *http.Request) {
	_, r, ok := a.who(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	to, err := strconv.Atoi(q.Get("to"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "to: a release number")
		return
	}
	from := 0
	if raw := q.Get("from"); raw != "" {
		if from, err = strconv.Atoi(raw); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "from: a release number")
			return
		}
	}
	out, err := a.svc().Compare(r.Context(), r.PathValue(paramSite), from, to)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// applicationAddresses handles GET /api/v1/portal/application-addresses/{name}.
//
// @Summary      Where an application answers
// @Description  The application's page origin and the domains mapped to it.
// @Tags         Sites
// @Produce      json
// @Param        name  path      string  true  "Application name"
// @Success      200   {object}  ApplicationAddresses
// @Failure      404   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/application-addresses/{name} [get]
func (a *portalAPI) applicationAddresses(w http.ResponseWriter, r *http.Request) {
	origin, domains, err := a.d.Runtime.ApplicationAddresses(r.Context(), r.PathValue("name"))
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, "no such application")
		return
	}
	writeJSON(w, http.StatusOK, ApplicationAddresses{PageOrigin: origin, Domains: domains})
}

// deployments handles GET /api/v1/portal/sites/{site}/deployments.
//
// @Summary      A site's history
// @Description  Every move of the site's environments, newest first, or of one environment. Someone who does not manage the site sees the moves of the environments they can open.
// @Tags         Sites
// @Produce      json
// @Param        site         path      string  true   "Site name or id"
// @Param        environment  query     string  false  "One environment"
// @Success      200          {object}  DeploymentList
// @Failure      404          {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/deployments [get]
func (a *portalAPI) deployments(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	env, _ := site.CanonicalEnvironment(r.URL.Query().Get("environment"))
	list, err := a.svc().Store.Deployments(r.Context(), ss.ID, env, 0)
	if err != nil {
		fail(w, r, err)
		return
	}
	list = slices.DeleteFunc(list, func(d site.Deployment) bool { return !ss.scope.sees(d.Environment) })
	if list == nil {
		list = []site.Deployment{}
	}
	writeJSON(w, http.StatusOK, DeploymentList{Deployments: list})
}
