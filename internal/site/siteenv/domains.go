package siteenv

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// DomainRole is a domain's place among its environment's domains (#2687):
// whether it is the primary, and whether it serves the release or redirects
// to the primary. Nil Primary and empty Mode leave each as it is.
type DomainRole struct {
	Primary *bool
	Mode    string
}

func (r DomainRole) set() bool { return r.Primary != nil || r.Mode != "" }

// AddDomainWithRole adds a domain as AddDomain does, then gives it role, checked
// before the domain is added, so a domain the role refuses is never added.
func (o Ops) AddDomainWithRole(ctx context.Context, actor sitesvc.Actor, req sitesvc.DomainRequest, role DomainRole) (*DomainResult, error) {
	if err := o.checkNewRole(ctx, req, role); err != nil {
		return nil, err
	}
	res, err := o.AddDomain(ctx, actor, req)
	if err != nil || !role.set() || res == nil || res.Domain == nil {
		return res, err
	}
	d, err := o.SetDomainRole(ctx, actor, res.Hostname, role)
	if err != nil {
		return nil, err
	}
	res.Domain = d
	return res, nil
}

// checkNewRole refuses, before a domain is added, a role it could not take.
func (o Ops) checkNewRole(ctx context.Context, req sitesvc.DomainRequest, role DomainRole) error {
	if !role.set() {
		return nil
	}
	if req.Application != "" {
		return refused("an application's domain answers by the application's own rules: it is never a primary and never redirects")
	}
	st, err := o.Find(ctx, req.Site)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	name, _ := site.CanonicalEnvironment(req.Environment)
	peers, err := o.peers(ctx, st.ID, name, "")
	if err != nil {
		return err
	}
	return checkRole(&site.Domain{Hostname: site.NormalizeHost(req.Hostname), Mode: site.DomainServe}, role, peers)
}

// SetDomainRole makes a site's domain its environment's primary or not, and
// sets whether it serves or redirects. It takes effect at once on every
// replica, and no promotion or rollback touches it: it belongs to the
// environment's domains, not to a release.
func (o Ops) SetDomainRole(ctx context.Context, actor sitesvc.Actor, hostname string, role DomainRole) (*site.Domain, error) {
	d, _, err := o.domainOf(ctx, actor, hostname)
	if err != nil {
		return nil, err
	}
	peers, err := o.peers(ctx, d.SiteID, d.Environment, d.Hostname)
	if err != nil {
		return nil, err
	}
	if err := checkRole(d, role, peers); err != nil {
		return nil, err
	}
	primary, mode := applied(d, role)
	if err := o.Store.SetDomainRole(ctx, d.Hostname, primary, mode); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	o.invalidateHosts(d, peers)
	return o.Store.Domain(ctx, d.Hostname) //nolint:wrapcheck // read by identity
}

// DomainChange is what one set_domain asks of a domain: its access (Public
// nil leaves it alone, Reason goes with a request to make it public) and its
// role.
type DomainChange struct {
	Public *bool
	Reason string
	Role   DomainRole
}

// SetDomain changes a domain's access, its role among its environment's
// domains, or both: the role first, so a domain made primary and public in one
// request is primary when it opens.
func (o Ops) SetDomain(ctx context.Context, actor sitesvc.Actor, hostname string, c DomainChange) (*DomainResult, error) {
	if c.Public == nil && !c.Role.set() {
		return nil, refused("say public, primary or mode")
	}
	out := &DomainResult{DomainResult: &sitesvc.DomainResult{Status: StatusDone}}
	if c.Role.set() {
		d, err := o.SetDomainRole(ctx, actor, hostname, c.Role)
		if err != nil {
			return nil, err
		}
		out.Domain = d
	}
	if c.Public == nil {
		return out, nil
	}
	return o.SetDomainAccess(ctx, actor, hostname, *c.Public, c.Reason)
}

// RemoveDomain is the service's, refused for a primary while domains of its
// environment redirect to it.
func (o Ops) RemoveDomain(ctx context.Context, actor sitesvc.Actor, hostname string) error {
	d, err := o.Store.Domain(ctx, site.NormalizeHost(hostname))
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if d.Primary {
		peers, err := o.peers(ctx, d.SiteID, d.Environment, d.Hostname)
		if err != nil {
			return err
		}
		if names := redirectNames(peers); len(names) > 0 {
			return refused("%s is the primary that %s redirect to: make another domain of environment %q primary, or set those to serve, before removing it",
				d.Hostname, strings.Join(names, ", "), d.Environment)
		}
	}
	return o.Service.RemoveDomain(ctx, actor, hostname) //nolint:wrapcheck // the service's answer
}

// domainOf is a site's domain and its site, for someone who manages the site.
func (o Ops) domainOf(ctx context.Context, actor sitesvc.Actor, hostname string) (*site.Domain, *site.Site, error) {
	d, err := o.Store.Domain(ctx, site.NormalizeHost(hostname))
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if d.SiteID == "" {
		return nil, nil, refused("%s maps to an application, which answers by its own rules: it is never a primary and never redirects", d.Hostname)
	}
	st, err := o.Store.Get(ctx, d.SiteID)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if err := o.MayEdit(ctx, actor, st); err != nil {
		return nil, nil, err //nolint:wrapcheck // the service's answer
	}
	return d, st, nil
}

// peers are the other domains of one environment.
func (o Ops) peers(ctx context.Context, siteID, env, except string) ([]site.Domain, error) {
	all, err := o.Store.SiteDomains(ctx, siteID)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	out := []site.Domain{}
	for _, p := range all {
		if p.Environment == env && p.Hostname != except {
			out = append(out, p)
		}
	}
	return out, nil
}

// applied is the primary mark and mode d has once role is applied. A domain
// made primary serves: a primary never redirects.
func applied(d *site.Domain, role DomainRole) (primary bool, mode string) {
	primary, mode = d.Primary, d.Mode
	if mode == "" {
		mode = site.DomainServe
	}
	if role.Primary != nil {
		primary = *role.Primary
		if primary && role.Mode == "" {
			mode = site.DomainServe
		}
	}
	if role.Mode != "" {
		mode = role.Mode
	}
	return primary, mode
}

// checkRole refuses a role that would leave the environment's domains
// inconsistent: an unknown mode, a primary that redirects, a redirect with no
// primary to go to, and a primary given up while others redirect to it.
func checkRole(d *site.Domain, role DomainRole, peers []site.Domain) error {
	if role.Mode != "" && !site.ValidMode(role.Mode) {
		return refused("mode %q: a domain serves the release (serve) or redirects to its environment's primary (redirect)", role.Mode)
	}
	primary, mode := applied(d, role)
	if mode == site.DomainRedirect {
		if err := checkRedirect(d, primary, peers); err != nil {
			return err
		}
	}
	if d.Primary && !primary {
		if names := redirectNames(peers); len(names) > 0 {
			return refused("%s is the primary that %s redirect to: make another domain primary, or set those to serve, first",
				d.Hostname, strings.Join(names, ", "))
		}
	}
	return nil
}

// checkRedirect refuses a redirect from a domain that would be the primary, or
// on an environment with no primary to redirect to.
func checkRedirect(d *site.Domain, primary bool, peers []site.Domain) error {
	if primary {
		return refused("%s cannot be the primary and redirect: the primary is where the others redirect to", d.Hostname)
	}
	for _, p := range peers {
		if p.Primary {
			return nil
		}
	}
	return refused("%s cannot redirect: environment %q has no primary domain to redirect to; make one of its domains primary first", d.Hostname, d.Environment)
}

// redirectNames names the domains among peers that redirect.
func redirectNames(peers []site.Domain) []string {
	var out []string
	for _, p := range peers {
		if p.Mode == site.DomainRedirect {
			out = append(out, p.Hostname)
		}
	}
	return out
}

// invalidateHosts has every replica re-read d and the domains beside it: a
// new primary changes where each redirect domain sends its visitors.
func (o Ops) invalidateHosts(d *site.Domain, peers []site.Domain) {
	if o.Invalidate == nil {
		return
	}
	o.Invalidate(d.Hostname)
	for _, p := range peers {
		o.Invalidate(p.Hostname)
	}
}
