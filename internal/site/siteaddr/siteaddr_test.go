package siteaddr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// TestPagesAddresses_LeavesOutAReservedLabel pins #2611: a two-character
// site's <site>--<env> is not offered for an ingress rule, since no
// certificate can be issued for it; its main address is.
func TestPagesAddresses_LeavesOutAReservedLabel(t *testing.T) {
	st := &site.Site{Name: "ab", MainEnvironment: site.EnvDev, Environments: []site.Environment{{Name: site.EnvDev}, {Name: "prod"}}}
	got := pagesAddresses(st, "pages.example.net")
	require.Len(t, got, 1)
	assert.Equal(t, "ab.pages.example.net", got[0].Hostname)
	assert.Len(t, pagesAddresses(&site.Site{Name: "abc", Environments: st.Environments}, "pages.example.net"), 2)
}

// TestList_CarriesASiteDomainsRole pins #2687: a site domain carries its role,
// a domain from before it reads as serve, and an application's domain and a
// pages address carry none.
func TestList_CarriesASiteDomainsRole(t *testing.T) {
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	domains := []site.Domain{
		{Hostname: "www.example.com", SiteID: "s1", Site: "m", Environment: "prod", Mode: site.DomainRedirect, RedirectsTo: "example.com", CreatedAt: at},
		{Hostname: "example.com", SiteID: "s1", Site: "m", Environment: "prod", Primary: true, CreatedAt: at},
		{Hostname: "shop.example.com", ApplicationID: "a1", Application: "shop", CreatedAt: at},
	}
	sites := []site.Site{{Name: "m", MainEnvironment: "prod", Environments: []site.Environment{{Name: "prod"}}, CreatedAt: at}}
	got := List(domains, sites, "pages.example.net").Addresses
	by := map[string]DeploymentAddress{}
	for _, a := range got {
		by[a.Hostname] = a
	}
	assert.Equal(t, "example.com", got[0].Hostname, "in hostname order")
	assert.True(t, by["example.com"].Primary)
	assert.Equal(t, site.DomainServe, by["example.com"].Mode)
	assert.Equal(t, "example.com", by["www.example.com"].RedirectsTo)
	assert.Empty(t, by["shop.example.com"].Mode)
	assert.Equal(t, KindApplication, by["shop.example.com"].Kind)
	assert.Empty(t, by["m.pages.example.net"].Mode)
	assert.Equal(t, "2026-11-17T00:00:00Z", by["m.pages.example.net"].CreatedAt)
	assert.Len(t, List(domains, sites, "").Addresses, 3, "no pages domain, no pages address")
}
