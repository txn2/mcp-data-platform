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

func TestSettings_ReadsEachBoundAndFillsItsDefault(t *testing.T) {
	set := Config{
		PagesDomain: "pages.example.net", MaxFiles: 7, MaxFileBytes: 8, MaxReleaseBytes: 9,
		Crawl: CrawlConfig{MaxPages: 10, MaxBytes: 11},
	}
	assert.Equal(t, Settings{
		PagesDomain: "pages.example.net", MaxFiles: 7, MaxFileBytes: 8, MaxReleaseBytes: 9,
		CrawlMaxPages: 10, CrawlMaxBytes: 11,
	}, set.Settings())

	assert.Equal(t, Settings{
		MaxFiles: DefaultMaxFiles, MaxFileBytes: DefaultMaxFileBytes, MaxReleaseBytes: DefaultMaxReleaseBytes,
		CrawlMaxPages: DefaultCrawlPages, CrawlMaxBytes: DefaultCrawlBytes,
	}, Config{}.Settings(), "an unset bound reads as its default")
}

func TestWith_SavedSettingsReplaceTheFileAndLeaveTheRest(t *testing.T) {
	on := true
	base := Config{
		Enabled: &on, PagesDomain: "file.example.net", MaxFiles: 1, MaxFileBytes: 2, MaxReleaseBytes: 3,
		Crawl: CrawlConfig{AllowPrivateCIDRs: []string{"10.0.0.0/8"}, MaxPages: 4, MaxBytes: 5},
		Cache: CacheConfig{MemoryBytes: 6, DiskBytes: 7, Dir: "/var/cache/sites"},
	}
	saved := Settings{PagesDomain: "saved.example.net", MaxFiles: 11, MaxFileBytes: 12, MaxReleaseBytes: 13, CrawlMaxPages: 14, CrawlMaxBytes: 15}

	got := base.With(saved)
	assert.Equal(t, saved, got.Settings(), "every saved value is in force")
	assert.Equal(t, base.Cache, got.Cache, "the cache is not a saved setting")
	assert.Equal(t, base.Crawl.AllowPrivateCIDRs, got.Crawl.AllowPrivateCIDRs, "the private ranges are not a saved setting")
	assert.Same(t, base.Enabled, got.Enabled)
	assert.Equal(t, "file.example.net", base.PagesDomain, "the file's config is left as it was")
}

func TestLive_SwapsTheConfigInForce(t *testing.T) {
	first := Config{PagesDomain: "first.example.net"}
	live := NewLive(first)
	assert.Equal(t, first, live.Get())

	second := Config{PagesDomain: "second.example.net"}
	live.Set(second)
	assert.Equal(t, second, live.Get())
}
