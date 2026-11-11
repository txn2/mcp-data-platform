// Package siteconfig is the sites: configuration section (#2517, #2518).
package siteconfig

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// DefaultKeepReleases is how many releases each environment keeps when
	// neither it nor the deployment says otherwise (#2602).
	DefaultKeepReleases = 50
	// DefaultOrphanGrace is how old an object no release names must be before
	// a sweep deletes it: past the longest a publish can take between storing
	// a file and recording its release.
	DefaultOrphanGrace = 24 * time.Hour
	// DefaultSweepEvery is how often the release sweep runs.
	DefaultSweepEvery = 24 * time.Hour
	// MaxKeepReleases bounds a limit: past it, keep every release with 0.
	MaxKeepReleases = 100000
)

// The release sweep's modes (#2602): delete prunes, report only counts what a
// delete would remove.
const (
	ModeDelete = "delete"
	ModeReport = "report"
)

// Config is the `sites:` section (#2517, #2518).
type Config struct {
	// Enabled is on wherever there is a database and a portal bucket.
	Enabled *bool `yaml:"enabled"`
	// PagesDomain is the parent of every application's page origin
	// (<app>.<pages_domain>) and every site's own address
	// (<site>.<pages_domain>). Empty serves neither. It should be a
	// registrable domain of its own: a sibling of the portal is same-site.
	PagesDomain     string      `yaml:"pages_domain"`
	MaxFiles        int         `yaml:"max_release_files"`
	MaxFileBytes    int64       `yaml:"max_file_bytes"`
	MaxReleaseBytes int64       `yaml:"max_release_bytes"`
	Cache           CacheConfig `yaml:"cache"`
	Crawl           CrawlConfig `yaml:"crawl"`
	// Releases is how many releases an environment keeps, and the sweep that
	// prunes the rest (#2602).
	Releases ReleasesConfig `yaml:"releases"`
	// ReservedHostnames are hostnames, and everything under them, that no
	// domain may claim, because the deployment answers on them itself: an
	// internal service name or a second address for the platform. A domain is
	// answered ahead of the platform's own routes.
	ReservedHostnames []string `yaml:"reserved_hostnames"`
}

// CacheConfig bounds a replica's cache of served files.
type CacheConfig struct {
	MemoryBytes int64 `yaml:"memory_bytes"`
	// Dir is the on-disk cache's directory; empty uses the system's.
	Dir       string `yaml:"dir"`
	DiskBytes int64  `yaml:"disk_bytes"`
}

// CrawlConfig bounds the migration crawl.
type CrawlConfig struct {
	AllowPrivateCIDRs []string `yaml:"allow_private_cidrs"`
	MaxPages          int      `yaml:"max_pages"`
	MaxBytes          int64    `yaml:"max_bytes"`
}

// ReleasesConfig is release retention (#2602).
type ReleasesConfig struct {
	// KeepDefault is how many releases an environment with no limit of its
	// own keeps; unset is DefaultKeepReleases and 0 keeps every release.
	KeepDefault *int `yaml:"keep_default"`
	// Mode is ModeDelete (the default) or ModeReport.
	Mode string `yaml:"mode"`
	// OrphanGrace is how old an object no release names must be before it is
	// deleted; unset is DefaultOrphanGrace.
	OrphanGrace time.Duration `yaml:"orphan_grace"`
	// Every is how often the sweep runs; unset is DefaultSweepEvery.
	Every time.Duration `yaml:"every"`
}

// Keep is the deployment's default limit.
func (r ReleasesConfig) Keep() int {
	if r.KeepDefault == nil {
		return DefaultKeepReleases
	}
	return *r.KeepDefault
}

// SweepMode is the mode the sweep runs in.
func (r ReleasesConfig) SweepMode() string {
	if r.Mode == "" {
		return ModeDelete
	}
	return r.Mode
}

// Grace is how old an orphan must be.
func (r ReleasesConfig) Grace() time.Duration {
	if r.OrphanGrace <= 0 {
		return DefaultOrphanGrace
	}
	return r.OrphanGrace
}

// Interval is how often the sweep runs.
func (r ReleasesConfig) Interval() time.Duration {
	if r.Every <= 0 {
		return DefaultSweepEvery
	}
	return r.Every
}

// validate refuses a limit or a mode the sweep cannot take.
func (r ReleasesConfig) validate() error {
	if k := r.Keep(); k < 0 || k > MaxKeepReleases {
		return fmt.Errorf("sites.releases.keep_default: 0 (keep every release) to %d, not %d", MaxKeepReleases, k)
	}
	if m := r.SweepMode(); m != ModeDelete && m != ModeReport {
		return fmt.Errorf("sites.releases.mode: %q or %q, not %q", ModeDelete, ModeReport, m)
	}
	return nil
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
// host, or would receive the portal's session cookie, and a release limit or
// sweep mode out of range.
func (c Config) Validate(portalHost, cookieDomain string) error {
	if err := c.Releases.validate(); err != nil {
		return err
	}
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
	return nil
}
