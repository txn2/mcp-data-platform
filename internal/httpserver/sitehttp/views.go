package sitehttp

import (
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
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
	// Address is the site's main address, "" with no pages domain or when the
	// main address is given to no environment.
	Address string `json:"address"`
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
	AddressNote string        `json:"address_note,omitempty"`
	Domains     []site.Domain `json:"domains"`
	// Permissions is what the caller may do to this site.
	Permissions sitesvc.Permissions `json:"permissions"`
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
}
