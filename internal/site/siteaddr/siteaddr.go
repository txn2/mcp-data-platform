// Package siteaddr is every hostname the deployment answers on for a site or
// an application, as the system administrator's ingress rules need them: each
// site's addresses on the pages domain and every domain added, with a site
// domain's role (#2687). GET /api/v1/admin/sites/domains answers it.
package siteaddr

import (
	"cmp"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/namesort"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// The kinds of address.
const (
	KindSite        = "site_address"
	KindDomain      = "domain"
	KindApplication = "application"
)

// TimeLayout is how an address's creation time is written.
const TimeLayout = "2006-01-02T15:04:05Z"

// DeploymentAddress is one hostname the deployment answers on for a site or an
// application. Each needs an ingress rule, added by a system administrator.
type DeploymentAddress struct {
	Hostname    string `json:"hostname" example:"marketing.pages.example.net"`
	Kind        string `json:"kind" example:"site_address"`
	Site        string `json:"site,omitempty" example:"marketing"`
	Environment string `json:"environment,omitempty" example:"default"`
	Application string `json:"application,omitempty"`
	// Primary, Mode and RedirectsTo are a site domain's role (#2687): a
	// redirect domain needs the same ingress as any other, and the gateway
	// answers the redirect. Absent on a pages address and an application's
	// domain.
	Primary     bool   `json:"primary,omitempty"`
	Mode        string `json:"mode,omitempty" enums:"serve,redirect"`
	RedirectsTo string `json:"redirects_to,omitempty"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// AddressList is every site address and domain on the deployment.
type AddressList struct {
	Addresses []DeploymentAddress `json:"addresses"`
}

// List is every domain and, with a pages domain, every site's addresses on
// it, in hostname order.
func List(domains []site.Domain, sites []site.Site, pagesDomain string) AddressList {
	out := make([]DeploymentAddress, 0, len(domains))
	for i := range domains {
		out = append(out, of(&domains[i]))
	}
	if pagesDomain != "" {
		for i := range sites {
			out = append(out, pagesAddresses(&sites[i], pagesDomain)...)
		}
	}
	slices.SortFunc(out, func(x, y DeploymentAddress) int { return namesort.Compare(x.Hostname, y.Hostname) })
	return AddressList{Addresses: out}
}

func of(d *site.Domain) DeploymentAddress {
	kind := KindDomain
	if d.Application != "" {
		kind = KindApplication
	}
	out := DeploymentAddress{
		Hostname: d.Hostname, Kind: kind, Site: d.Site, Environment: d.Environment, Application: d.Application,
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt.UTC().Format(TimeLayout),
	}
	if d.SiteID != "" {
		out.Primary, out.Mode, out.RedirectsTo = d.Primary, cmp.Or(d.Mode, site.DomainServe), d.RedirectsTo
	}
	return out
}

// pagesAddresses is a site's addresses on the pages domain: the main address
// and every environment's <site>--<env>, leaving out a <site>--<env> no
// certificate can be issued for, which an ingress rule would only fail on
// (#2611).
func pagesAddresses(st *site.Site, pages string) []DeploymentAddress {
	at := st.CreatedAt.UTC().Format(TimeLayout)
	out := make([]DeploymentAddress, 0, len(st.Environments))
	if st.MainEnvironment != "" {
		out = append(out, DeploymentAddress{
			Hostname: st.Name + "." + pages, Kind: KindSite, Site: st.Name, Environment: st.MainEnvironment, CreatedBy: st.CreatedBy, CreatedAt: at,
		})
	}
	for _, e := range st.Environments {
		label := site.EnvLabel(st.Name, e.Name)
		if site.ReservedLabel(label) {
			continue
		}
		out = append(out, DeploymentAddress{
			Hostname: label + "." + pages, Kind: KindSite, Site: st.Name, Environment: e.Name, CreatedBy: st.CreatedBy, CreatedAt: at,
		})
	}
	return out
}
