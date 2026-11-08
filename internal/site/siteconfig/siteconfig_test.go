package siteconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsEnabled(t *testing.T) {
	on, off := true, false
	assert.True(t, Config{}.IsEnabled(), "unset is on")
	assert.True(t, Config{Enabled: &on}.IsEnabled())
	assert.False(t, Config{Enabled: &off}.IsEnabled())
}

func TestLimits_FillsEveryUnsetBound(t *testing.T) {
	for name, c := range map[string]Config{
		"zero": {},
		"negative": {
			MaxFiles: -1, MaxFileBytes: -1, MaxReleaseBytes: -1,
			Crawl: CrawlConfig{MaxPages: -1, MaxBytes: -1},
			Cache: CacheConfig{MemoryBytes: -1, DiskBytes: -1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := c.Limits()
			assert.Equal(t, DefaultMaxFiles, got.MaxFiles)
			assert.Equal(t, DefaultMaxFileBytes, got.MaxFileBytes)
			assert.Equal(t, DefaultMaxReleaseBytes, got.MaxReleaseBytes)
			assert.Equal(t, DefaultCrawlPages, got.Crawl.MaxPages)
			assert.Equal(t, DefaultCrawlBytes, got.Crawl.MaxBytes)
			assert.Equal(t, DefaultMemoryCache, got.Cache.MemoryBytes)
			assert.Equal(t, DefaultDiskCache, got.Cache.DiskBytes)
		})
	}
}

func TestLimits_KeepsWhatIsSet(t *testing.T) {
	c := Config{
		PagesDomain: "pages.example.net", MaxFiles: 7, MaxFileBytes: 8, MaxReleaseBytes: 9,
		Crawl: CrawlConfig{MaxPages: 10, MaxBytes: 11},
		Cache: CacheConfig{MemoryBytes: 12, DiskBytes: 13, Dir: "/var/cache/sites"},
	}
	assert.Equal(t, c, c.Limits())
}

func TestValidate(t *testing.T) {
	acme := func(address string) TLSConfig {
		return TLSConfig{ACME: ACMEConfig{Enabled: true, Address: address}}
	}
	cases := []struct {
		name         string
		cfg          Config
		portal       string
		cookieDomain string
		want         string
	}{
		{name: "nothing configured", portal: "portal.example.com"},
		{name: "a domain of its own", cfg: Config{PagesDomain: "pages.example.net"}, portal: "portal.example.com", cookieDomain: ".example.com"},
		{name: "a sibling with no cookie domain", cfg: Config{PagesDomain: "pages.example.com"}, portal: "portal.example.com"},
		{name: "not a DNS name", cfg: Config{PagesDomain: "*.example.net"}, want: "sites.pages_domain:"},
		{name: "one label", cfg: Config{PagesDomain: "pages"}, want: "sites.pages_domain:"},
		{name: "the portal's host", cfg: Config{PagesDomain: "portal.example.com"}, portal: "portal.example.com", want: "holds the portal's host"},
		{name: "the portal under it", cfg: Config{PagesDomain: "example.com"}, portal: "portal.example.com", want: "holds the portal's host"},
		{name: "under the cookie domain", cfg: Config{PagesDomain: "pages.example.com"}, portal: "portal.example.com", cookieDomain: ".example.com", want: "session cookie's domain example.com"},
		{name: "under a cookie domain with no dot", cfg: Config{PagesDomain: "pages.example.com"}, portal: "portal.example.com", cookieDomain: "example.com", want: "session cookie's domain"},
		{name: "the cookie domain itself", cfg: Config{PagesDomain: "example.org"}, portal: "portal.example.com", cookieDomain: "example.org", want: "session cookie's domain"},
		{name: "acme with an address", cfg: Config{TLS: acme(":8443")}},
		{name: "acme with no address", cfg: Config{TLS: acme("")}, want: "sites.tls.acme.address"},
		{name: "acme off with no address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(tc.portal, tc.cookieDomain)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCheck_ReadsThePortalsHostFromItsURL(t *testing.T) {
	c := Config{PagesDomain: "example.com"}
	cases := []struct {
		name, url string
		refused   bool
	}{
		{"a host under the pages domain", "https://portal.example.com/portal", true},
		{"with a port", "https://portal.example.com:8443", true},
		{"in capitals", "https://PORTAL.Example.COM", true},
		{"somewhere else", "https://portal.example.net", false},
		{"no portal configured", "", false},
		{"an address that does not parse", "http://[bad", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Check(tc.url, "")
			if tc.refused {
				require.ErrorContains(t, err, "holds the portal's host portal.example.com")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCheck_PassesTheCookieDomainThrough(t *testing.T) {
	err := Config{PagesDomain: "pages.example.com"}.Check("https://portal.example.net", ".example.com")
	require.ErrorContains(t, err, "session cookie's domain")
}
