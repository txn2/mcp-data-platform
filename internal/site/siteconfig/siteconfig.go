// Package siteconfig is the sites: configuration section (#2517, #2518).
package siteconfig

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Publishing's defaults.
const (
	DefaultMaxFiles        = 20000
	DefaultMaxFileBytes    = int64(64 << 20)
	DefaultMaxReleaseBytes = int64(2 << 30)
	DefaultCrawlPages      = 2000
	DefaultCrawlBytes      = int64(512 << 20)
	DefaultMemoryCache     = int64(256 << 20)
	DefaultDiskCache       = int64(2 << 30)
)

// Config is the `sites:` section (#2517, #2518).
type Config struct {
	// Enabled is on wherever there is a database and a portal bucket.
	Enabled *bool `yaml:"enabled"`
	// PagesDomain is the parent of every application's page origin
	// (<app>.<pages_domain>) and every environment's preview
	// (<env>--<site>.<pages_domain>). Empty serves neither. It should be a
	// registrable domain of its own: a sibling of the portal is same-site.
	PagesDomain     string      `yaml:"pages_domain"`
	MaxFiles        int         `yaml:"max_release_files"`
	MaxFileBytes    int64       `yaml:"max_file_bytes"`
	MaxReleaseBytes int64       `yaml:"max_release_bytes"`
	Cache           CacheConfig `yaml:"cache"`
	Verify          NetConfig   `yaml:"verify"`
	Crawl           CrawlConfig `yaml:"crawl"`
	TLS             TLSConfig   `yaml:"tls"`
}

// CacheConfig bounds a replica's cache of served files.
type CacheConfig struct {
	MemoryBytes int64 `yaml:"memory_bytes"`
	// Dir is the on-disk cache's directory; empty uses the system's.
	Dir       string `yaml:"dir"`
	DiskBytes int64  `yaml:"disk_bytes"`
}

// NetConfig is what a server-side check of a hostname may reach.
type NetConfig struct {
	AllowPrivateCIDRs []string `yaml:"allow_private_cidrs"`
	// DNSServer is the host:port TXT lookups go to, empty for the system's.
	DNSServer string `yaml:"dns_server"`
}

// CrawlConfig bounds the migration crawl.
type CrawlConfig struct {
	AllowPrivateCIDRs []string `yaml:"allow_private_cidrs"`
	MaxPages          int      `yaml:"max_pages"`
	MaxBytes          int64    `yaml:"max_bytes"`
}

// IsEnabled reports whether sites run where they can.
func (c Config) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Limits is the config with every bound at its default when unset.
func (c Config) Limits() Config {
	if c.MaxFiles <= 0 {
		c.MaxFiles = DefaultMaxFiles
	}
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = DefaultMaxFileBytes
	}
	if c.MaxReleaseBytes <= 0 {
		c.MaxReleaseBytes = DefaultMaxReleaseBytes
	}
	if c.Crawl.MaxPages <= 0 {
		c.Crawl.MaxPages = DefaultCrawlPages
	}
	if c.Crawl.MaxBytes <= 0 {
		c.Crawl.MaxBytes = DefaultCrawlBytes
	}
	if c.Cache.MemoryBytes <= 0 {
		c.Cache.MemoryBytes = DefaultMemoryCache
	}
	if c.Cache.DiskBytes <= 0 {
		c.Cache.DiskBytes = DefaultDiskCache
	}
	if c.Cache.Dir == "" {
		c.Cache.Dir = filepath.Join(os.TempDir(), "mcp-data-platform-sites")
	}
	return c
}

// Check is Validate over the portal's address as configured.
func (c Config) Check(portalURL, cookieDomain string) error {
	host := ""
	if u, err := url.Parse(portalURL); err == nil {
		host = site.NormalizeHost(u.Hostname())
	}
	return c.Validate(host, cookieDomain)
}

// Validate refuses a pages domain that is not a DNS name, is the portal's
// host, or would receive the portal's session cookie.
func (c Config) Validate(portalHost, cookieDomain string) error {
	if c.PagesDomain != "" {
		if err := site.ValidateHostname(c.PagesDomain); err != nil {
			return fmt.Errorf("sites.pages_domain: %w", err)
		}
		if c.PagesDomain == portalHost || site.SameOrUnder(portalHost, c.PagesDomain) {
			return fmt.Errorf("sites.pages_domain %s holds the portal's host %s, so a page could be served where the portal is", c.PagesDomain, portalHost)
		}
		if d := strings.TrimPrefix(cookieDomain, "."); site.SameOrUnder(c.PagesDomain, d) {
			return fmt.Errorf("sites.pages_domain %s is under the session cookie's domain %s, so pages would receive the portal's session", c.PagesDomain, d)
		}
	}
	if c.TLS.ACME.Enabled && c.TLS.ACME.Address == "" {
		return errors.New("sites.tls.acme.address: the listener's address is required when acme is enabled")
	}
	return nil
}
