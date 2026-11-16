// Package siterouting is the Site routing integration's items (#2659): every
// hostname the platform serves for sites and applications, computed from what
// is stored, never from what a call site says it changed. Each environment's
// address on the pages domain, each application's page origin, and each added
// domain, as the request's hosts entries.
package siterouting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// The kinds of hostname.
const (
	KindPages       = "pages"
	KindApplication = "application"
	KindDomain      = "domain"
)

// Source reads what the hostnames are computed from.
type Source struct {
	Sites      func(ctx context.Context) ([]site.Site, error)
	Domains    func(ctx context.Context) ([]site.Domain, error)
	Apps       func(ctx context.Context) ([]application.Application, error)
	Addressing func() site.Addressing
}

// Items implements integration.Source: every hostname served now, sorted by
// hostname. A hostname reached two ways (a domain equal to a pages address,
// which the claim rules refuse) is listed once, as the first way it was met.
func (s Source) Items(ctx context.Context) ([]integration.Item, error) {
	c := collector{seen: map[string]bool{}, out: []integration.Item{}}
	addr := s.Addressing()
	sites, err := s.Sites(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sites: %w", err)
	}
	for i := range sites {
		st := &sites[i]
		for _, e := range st.Environments {
			c.add(addr.PagesAddress(st, e.Name), map[string]any{fieldKind: KindPages, "site": st.Name, "environment": e.Name, fieldPublic: false})
		}
	}
	apps, err := s.Apps(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing applications: %w", err)
	}
	for _, a := range apps {
		c.add(addr.PageOrigin(a.Name), map[string]any{fieldKind: KindApplication, "application": a.Name, fieldPublic: false})
	}
	domains, err := s.Domains(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}
	for _, d := range domains {
		c.add("https://"+d.Hostname, domainEntry(d))
	}
	sort.Slice(c.out, func(i, j int) bool { return c.out[i].Key < c.out[j].Key })
	return c.out, nil
}

// The entry fields naming what a hostname is and whether it answers anyone.
const (
	fieldKind   = "kind"
	fieldPublic = "public"
)

// collector gathers hostnames once each.
type collector struct {
	seen map[string]bool
	out  []integration.Item
}

// add records an origin's hostname with its entry, once.
func (c *collector) add(origin string, entry map[string]any) {
	host := hostOf(origin)
	if host == "" || c.seen[host] {
		return
	}
	c.seen[host] = true
	entry["hostname"] = host
	c.out = append(c.out, integration.Item{Key: host, Entry: entry})
}

// domainEntry is a domain's entry: the site environment or application it
// serves, and whether it answers anyone.
func domainEntry(d site.Domain) map[string]any {
	entry := map[string]any{fieldKind: KindDomain, fieldPublic: d.Public}
	if d.Application != "" {
		entry["application"] = d.Application
	} else {
		entry["site"], entry["environment"] = d.Site, d.Environment
	}
	return entry
}

// hostOf is an origin's hostname, "" for none.
func hostOf(origin string) string {
	if origin == "" {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// ErrNoRouting is a retry with no routing script chosen.
var ErrNoRouting = errors.New("no site routing script is chosen")

// Lookup is the Site routing integration as the places that show a hostname
// read it: each hostname's status while a script is chosen, and a retry. Its
// Service is set once the integrations are wired; nil, or a Lookup with no
// Service, shows nothing and retries nothing.
type Lookup struct {
	Service    *integration.Service
	Addressing func() site.Addressing
}

// Of is each host's status, nil when no script is chosen. An administrator
// reads the run error of a host the script itself failed; anyone else reads
// one sentence. A read that fails shows nothing rather than failing the page.
func (l *Lookup) Of(ctx context.Context, admin bool, hosts []string) map[string]*site.Routing {
	if l == nil || l.Service == nil || len(hosts) == 0 {
		return nil
	}
	got, err := l.Service.Statuses(ctx, integration.OpSiteRouting, hosts, admin)
	if err != nil {
		slog.WarnContext(ctx, "sites: reading routing statuses failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	if got == nil {
		return nil
	}
	out := make(map[string]*site.Routing, len(got))
	for h, st := range got {
		out[h] = &site.Routing{
			Status: st.Status, Message: st.Message, CheckedAt: st.CheckedAt, ScriptFailed: st.ScriptFailed,
			RunID: st.RunID, RunScriptID: st.RunScriptID, RunError: st.RunError,
		}
	}
	return out
}

// Domains is domains with each one's status, as they are when no script is
// chosen. The caller's slice is not changed.
func (l *Lookup) Domains(ctx context.Context, admin bool, domains []site.Domain) []site.Domain {
	hosts := make([]string, 0, len(domains))
	for _, d := range domains {
		hosts = append(hosts, d.Hostname)
	}
	return withStatuses(domains, l.Of(ctx, admin, hosts))
}

// Site is a site's domains with each one's status, and each environment's
// pages address status by environment, the second nil when no script is
// chosen.
func (l *Lookup) Site(ctx context.Context, admin bool, st *site.Site, domains []site.Domain) (routed []site.Domain, byEnv map[string]*site.Routing) {
	if l == nil || l.Service == nil {
		return domains, nil
	}
	pages := l.Addressing().PagesAddresses(st)
	statuses := l.Of(ctx, admin, siteHosts(pages, domains))
	if statuses == nil {
		return domains, nil
	}
	byEnv = make(map[string]*site.Routing, len(pages))
	for env, addr := range pages {
		if r := statuses[hostOf(addr)]; r != nil {
			byEnv[env] = r
		}
	}
	return withStatuses(domains, statuses), byEnv
}

// Origin is the status of an address's hostname, nil for none.
func (l *Lookup) Origin(ctx context.Context, admin bool, address string) *site.Routing {
	h := hostOf(address)
	if h == "" {
		return nil
	}
	return l.Of(ctx, admin, []string{h})[h]
}

// Retry asks the Site routing script to run again for someone who can see st
// and domains: at once for an administrator; for anyone else only when one of
// the site's hostnames failed, and not just after a run started
// (integration.Service.RetryFailed).
func (l *Lookup) Retry(ctx context.Context, admin bool, st *site.Site, domains []site.Domain) error {
	if l == nil || l.Service == nil {
		return ErrNoRouting
	}
	if admin {
		return l.Service.Retry(ctx, integration.OpSiteRouting) //nolint:wrapcheck // the integration names its refusal
	}
	return l.Service.RetryFailed(ctx, integration.OpSiteRouting, siteHosts(l.Addressing().PagesAddresses(st), domains)) //nolint:wrapcheck // the integration names its refusal
}

// Refusal is the HTTP status and sentence a retry Retry refused is answered
// with.
func (*Lookup) Refusal(err error) (status int, message string) {
	switch {
	case errors.Is(err, integration.ErrNothingFailed):
		return http.StatusConflict, "nothing of this site failed to route, so there is nothing to retry"
	case errors.Is(err, integration.ErrTooSoon):
		return http.StatusTooManyRequests, err.Error()
	case errors.Is(err, ErrNoRouting), errors.Is(err, integration.ErrNotBound):
		return http.StatusNotFound, "no site routing script is chosen"
	default:
		return http.StatusInternalServerError, "the retry could not be asked for"
	}
}

// siteHosts is a site's hostnames: its domains and its pages addresses.
func siteHosts(pages map[string]string, domains []site.Domain) []string {
	hosts := make([]string, 0, len(domains))
	for _, d := range domains {
		hosts = append(hosts, d.Hostname)
	}
	for _, addr := range pages {
		if h := hostOf(addr); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// withStatuses is domains with each one's status from statuses.
func withStatuses(domains []site.Domain, statuses map[string]*site.Routing) []site.Domain {
	if statuses == nil {
		return domains
	}
	out := make([]site.Domain, len(domains))
	for i, d := range domains {
		d.Routing = statuses[d.Hostname]
		out[i] = d
	}
	return out
}
