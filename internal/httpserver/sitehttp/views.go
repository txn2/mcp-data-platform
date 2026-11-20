package sitehttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile"
	"github.com/txn2/mcp-data-platform/internal/site/sitetraffic"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SiteLimits is what a release may hold, shown before an upload.
type SiteLimits struct {
	MaxFiles        int   `json:"max_release_files" example:"20000"`
	MaxFileBytes    int64 `json:"max_file_bytes" example:"67108864"`
	MaxReleaseBytes int64 `json:"max_release_bytes" example:"2147483648"`
	CrawlMaxPages   int   `json:"crawl_max_pages" example:"2000"`
	CrawlMaxBytes   int64 `json:"crawl_max_bytes" example:"536870912"`
}

// SiteView is a site as the portal shows it.
type SiteView struct {
	site.Site
	// Address is the site's production address: where the environment
	// holding the main address is reached, its public domain first, then
	// another domain, then its pages address (#2658); "" when the main address
	// is given to no environment.
	Address string `json:"address"`
	// MoreDomains is how many other domains that environment answers at.
	MoreDomains int `json:"more_domains"`
	// Addresses is where people reach each environment, by name: the first
	// of its domains, otherwise its address on the pages domain, "" for
	// none. manage_site get answers the same (#2607).
	Addresses map[string]string `json:"addresses"`
	// PagesAddresses is each environment's address on the pages domain, by
	// name: the main address for the environment holding it, otherwise
	// <site>--<env>; "" with no pages domain or for an address no certificate
	// can be issued for. It answers only where the deployment's ingress
	// routes it.
	PagesAddresses map[string]string `json:"pages_addresses"`
	// AddressNote says why the site's environments have no address on the
	// pages domain: a two-character site name (#2611). Absent otherwise.
	AddressNote string `json:"address_note,omitempty"`
	// EnvironmentNameRoom is the longest environment name the site can add:
	// its name and "--" leave the rest of one DNS label (#2636).
	EnvironmentNameRoom int           `json:"environment_name_room" example:"32"`
	Domains             []site.Domain `json:"domains"`
	// Applications are the applications that use the site, each with the
	// site's environments it holds (#2735), named only to a caller whose
	// persona reaches the Applications pages; [] otherwise and when none does.
	Applications []site.Application `json:"applications"`
	// PagesRouting is the Site routing script's status for each environment's
	// pages address, by environment (#2659); absent when no script is chosen,
	// and on the Sites list.
	PagesRouting map[string]*site.Routing `json:"pages_routing,omitempty"`
	// Permissions is what the caller may do to this site.
	Permissions sitesvc.Permissions `json:"permissions"`
	// Traffic is the site's last 24 hours over the environments the caller
	// sees (#2647), on the Sites list; absent elsewhere.
	Traffic *SiteTraffic `json:"traffic,omitempty"`
}

// SiteList is the Sites page.
type SiteList struct {
	Sites []SiteView `json:"sites"`
	Total int        `json:"total"`
	// PagesDomain is "" when the deployment has none, so sites have no address
	// of their own.
	PagesDomain string     `json:"pages_domain"`
	Limits      SiteLimits `json:"limits"`
	// CanCreate is whether the caller's persona may create a site.
	CanCreate bool `json:"can_create"`
	// Admin is whether the caller is an administrator.
	Admin bool `json:"admin"`
}

// SiteRequest creates a site.
type SiteRequest struct {
	Name        string `json:"name" example:"careers"`
	Description string `json:"description"`
}

// SiteUpdate changes a site's record; a field left out is left alone.
type SiteUpdate struct {
	Description *string `json:"description,omitempty"`
	Owner       *string `json:"owner_email,omitempty"`
	// Media, when set, is every resource folder the site serves at a URL
	// prefix (#2591); [] removes them all. Anyone who manages the site, for a
	// folder they can read.
	Media *[]site.Mount `json:"media,omitempty"`
}

// MainEnvironmentBody gives the main address to an environment, "" to none.
type MainEnvironmentBody struct {
	Environment string `json:"environment" example:"prod"`
}

// EnvironmentResponse is what creating or changing an environment did.
type EnvironmentResponse struct {
	Site   SiteView `json:"site"`
	Status string   `json:"status" example:"done"`
	Note   string   `json:"note,omitempty"`
	// Approval is the request a redirect waits on (status awaiting_approval).
	Approval *sitesvc.ApprovalRequest `json:"approval,omitempty"`
}

// TrafficPoint is one step of the Traffic view's series.
type TrafficPoint struct {
	At time.Time `json:"at"`
	sitetraffic.Totals
}

// TrafficView is a site's traffic over a range (#2647): requests by status
// class and latency percentiles over time, the range's totals, and the same by
// environment. Its figures are the platform's own, recorded by every replica
// into shared rows, so it needs no Prometheus backend.
type TrafficView struct {
	Range string `json:"range" example:"24h"`
	// StepSeconds is the width of each point of Series.
	StepSeconds int64 `json:"step_seconds" example:"900"`
	// Recording is false where sites.traffic.enabled is off; nothing is
	// recorded then.
	Recording    bool                          `json:"recording"`
	Environment  string                        `json:"environment,omitempty"`
	Total        sitetraffic.Totals            `json:"total"`
	Series       []TrafficPoint                `json:"series"`
	Environments map[string]sitetraffic.Totals `json:"environments"`
}

// SiteTraffic is the Sites list's figures for one site: its last 24 hours.
type SiteTraffic struct {
	Requests int64   `json:"requests_24h" example:"1200"`
	P95      float64 `json:"p95_ms" example:"48"`
}

// traffic handles GET /api/v1/portal/sites/{site}/traffic.
//
// @Summary      Read a site's traffic
// @Description  Requests by status class and p50, p95 and p99 latency over the last hour, 24 hours, 7 days or 30 days, the range's totals, and the same by environment (#2647). Recorded by the platform in its own database, so it needs no Prometheus backend and no grant beyond seeing the site; someone who does not manage the site sees the environments they can open. No figure holds anything a visitor's request carried.
// @Tags         Sites
// @Produce      json
// @Param        site         path   string  true   "Site name or id"
// @Param        range        query  string  false  "1h, 24h (default), 7d or 30d"
// @Param        environment  query  string  false  "One environment; absent for every one the caller sees"
// @Success      200  {object}  TrafficView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/traffic [get]
func (a *portalAPI) traffic(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	if !ss.scope.manage && len(ss.scope.open) == 0 {
		fail(w, r, site.ErrNotAllowed)
		return
	}
	rg, err := sitetraffic.RangeOf(r.URL.Query().Get("range"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	env, _ := site.CanonicalEnvironment(r.URL.Query().Get("environment"))
	if env != "" && !ss.scope.sees(env) {
		fail(w, r, noSuchEnvironment(env))
		return
	}
	out, err := a.trafficView(r.Context(), ss, rg, env)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func noSuchEnvironment(env string) error {
	return fmt.Errorf("environment %q: %w", env, site.ErrNoEnvironment)
}

// trafficView reads the view. Someone who does not manage the site reads only
// the environments they can open: the series is then the sum of those.
func (a *portalAPI) trafficView(ctx context.Context, ss *scopedSite, rg sitetraffic.Range, env string) (*TrafficView, error) {
	store := a.d.Runtime.TrafficStore
	out := &TrafficView{
		Range: rg.Name, StepSeconds: int64(rg.Step / time.Second), Recording: a.d.Runtime.Live.Get().Traffic.IsEnabled(),
		Environment: env, Series: []TrafficPoint{}, Environments: map[string]sitetraffic.Totals{},
	}
	if store == nil {
		return out, nil
	}
	since := rg.Since(time.Now())
	by, _, err := store.ByEnvironment(ctx, sitetraffic.KindSite, ss.ID, rg.Grain, since)
	if err != nil {
		return nil, err //nolint:wrapcheck // answered by fail
	}
	var total sitetraffic.Counts
	for e, c := range by {
		if !ss.scope.sees(e) {
			continue
		}
		out.Environments[e] = sitetraffic.TotalsOf(c)
		if env == "" || env == e {
			total.Add(c)
		}
	}
	out.Total = sitetraffic.TotalsOf(total)
	out.Series, err = seriesOf(ctx, store, sitetraffic.SeriesQuery{Kind: sitetraffic.KindSite, Subject: ss.ID, Range: rg, Since: since}, viewedEnvironments(ss, env))
	return out, err
}

// viewedEnvironments are the environments a series sums: the one asked for,
// every one ("") for someone who manages the site, or the ones they open.
func viewedEnvironments(ss *scopedSite, env string) []string {
	if env != "" || ss.scope.manage {
		return []string{env}
	}
	envs := make([]string, 0, len(ss.scope.open))
	for e := range ss.scope.open {
		envs = append(envs, e)
	}
	return envs
}

// seriesOf is the summed series of envs, in time order.
func seriesOf(ctx context.Context, store *sitetraffic.Store, q sitetraffic.SeriesQuery, envs []string) ([]TrafficPoint, error) {
	points := map[time.Time]*sitetraffic.Counts{}
	var order []time.Time
	for _, e := range envs {
		q.Environment = e
		series, err := store.Series(ctx, q)
		if err != nil {
			return nil, err //nolint:wrapcheck // answered by fail
		}
		for _, p := range series {
			if points[p.At] == nil {
				points[p.At] = &sitetraffic.Counts{}
				order = append(order, p.At)
			}
			points[p.At].Add(p.Counts)
		}
	}
	slices.SortFunc(order, time.Time.Compare)
	out := make([]TrafficPoint, 0, len(order))
	for _, at := range order {
		out = append(out, TrafficPoint{At: at, Totals: sitetraffic.TotalsOf(*points[at])})
	}
	return out, nil
}

// listTraffic is every site's last 24 hours, read once for the Sites list
// from hour rows, by environment so each caller's figure sums only the
// environments they see.
func (a *portalAPI) listTraffic(ctx context.Context) map[string]map[string]sitetraffic.Counts {
	store := a.d.Runtime.TrafficStore
	if store == nil {
		return nil
	}
	since := time.Now().UTC().Truncate(time.Hour).Add(-23 * time.Hour)
	by, err := store.BySubject(ctx, sitetraffic.KindSite, since)
	if err != nil {
		return nil
	}
	return by
}

// siteTraffic is one site's list figures over the environments in view.
func siteTraffic(by map[string]sitetraffic.Counts, st *site.Site) *SiteTraffic {
	var c sitetraffic.Counts
	for _, e := range st.Environments {
		c.Add(by[e.Name])
	}
	t := sitetraffic.TotalsOf(c)
	return &SiteTraffic{Requests: t.Requests, P95: t.P95}
}

// errNoTile answers an environment with no tile to show: none drawn yet for
// the release it serves, none it may have, or one the caller may not open.
const errNoTile = "this environment has no tile to show"

// tile serves an environment's tile.
//
// @Summary      An environment's tile
// @Description  The picture of the release an environment serves (#2658), drawn by the tile worker from the release's own files and redrawn when the release changes. Answered only to someone who may open the environment, by the rule its addresses apply; an environment with no release, offline or redirecting, or whose tile for its current release is not drawn yet, answers 404, which the portal shows as a placeholder.
// @Tags         Sites
// @Produce      png
// @Param        site  path  string  true  "Site name or id"
// @Param        name  path  string  true  "Environment"
// @Success      200
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sites/{site}/environments/{name}/tile [get]
func (a *portalAPI) tile(w http.ResponseWriter, r *http.Request) {
	ss, r, ok := a.siteAndScope(w, r)
	if !ok {
		return
	}
	body, ok := a.tileOf(r.Context(), ss, r.PathValue("name"))
	if !ok {
		httpjson.WriteError(w, http.StatusNotFound, errNoTile)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(body) // #nosec G705 -- a PNG the tile worker drew, served as image/png with nosniff
}

// tileOf is the tile of the environment the caller names, when they may open
// it and it is drawn for the release the environment serves now. A tile of
// another release is not shown: the portal says it is being drawn rather than
// show a picture of what the environment no longer serves.
func (a *portalAPI) tileOf(ctx context.Context, ss *scopedSite, raw string) ([]byte, bool) {
	name, _ := site.CanonicalEnvironment(raw)
	env := ss.Environment(name)
	tiles := a.d.Runtime.Tiles
	if env == nil || !ss.scope.sees(name) || env.Release == 0 || !env.Online() || tiles == nil {
		return nil, false
	}
	t, err := tiles.Get(ctx, ss.ID, name)
	if err != nil || t.Key == "" || t.Release != env.Release {
		return nil, false
	}
	body, _, err := a.svc().Objects.GetObject(ctx, a.svc().Bucket, t.Key)
	if err != nil {
		tileUnread(ctx, tiles, tileRef{site: ss.ID, environment: name, key: t.Key}, err)
		return nil, false
	}
	return body, true
}

// tileRef names one environment's recorded tile object.
type tileRef struct{ site, environment, key string }

// tileUnread handles a recorded tile whose object could not be read. One that
// is gone is forgotten, so the worker draws it again rather than the portal
// saying "not drawn yet" until the release changes (#2701); any other failure
// is logged and the row kept.
func tileUnread(ctx context.Context, tiles *sitetile.Store, t tileRef, err error) {
	if objectobs.Reason(err) != observability.StorageReasonNotFound {
		slog.WarnContext(ctx, "sites: reading a tile failed", "site", t.site,
			"environment", logsan.SanitizeForLog(t.environment), "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	if ferr := tiles.Forget(ctx, t.site, t.environment, t.key); ferr != nil {
		slog.WarnContext(ctx, "sites: forgetting a missing tile failed", "site", t.site,
			"environment", logsan.SanitizeForLog(t.environment), "error", logsan.SanitizeForLog(ferr.Error()))
	}
}
