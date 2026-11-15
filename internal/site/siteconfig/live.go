package siteconfig

import (
	"sync/atomic"
	"time"
)

// SettingsSection is the platform_settings section the portal saves.
const SettingsSection = "sites"

// Settings is what an administrator changes at run time. Once saved it
// replaces these values from the file, which stay the seed for a deployment
// that has saved none.
type Settings struct {
	PagesDomain     string `json:"pages_domain" example:"pages.example.net"`
	MaxFiles        int    `json:"max_release_files" example:"20000"`
	MaxFileBytes    int64  `json:"max_file_bytes" example:"67108864"`
	MaxReleaseBytes int64  `json:"max_release_bytes" example:"2147483648"`
	CrawlMaxPages   int    `json:"crawl_max_pages" example:"2000"`
	CrawlMaxBytes   int64  `json:"crawl_max_bytes" example:"536870912"`
	// ReleasesKeep is how many releases an environment with no limit of its
	// own keeps, 0 for every release (#2602). A section saved before it
	// existed carries none, and keeps the file's value.
	ReleasesKeep *int `json:"releases_keep,omitempty" example:"50"`
	// ReleasesMode is "delete" or "report"; empty keeps the file's value.
	ReleasesMode string `json:"releases_mode,omitempty" example:"delete"`
	// TrafficEnabled records each site's traffic (#2647); nil keeps the
	// file's value.
	TrafficEnabled *bool `json:"traffic_enabled,omitempty" example:"true"`
	// TrafficMinuteHours and TrafficHourDays are how long minute rows (in
	// hours) and hour rows (in days) are kept; nil keeps the file's value.
	TrafficMinuteHours *int `json:"traffic_minute_hours,omitempty" example:"24"`
	TrafficHourDays    *int `json:"traffic_hour_days,omitempty" example:"90"`
}

// Settings is the config's run-time values, every bound at its default when
// unset.
func (c Config) Settings() Settings {
	l := c.Limits()
	keep := l.Releases.Keep()
	traffic := l.Traffic.IsEnabled()
	minuteHours, hourDays := int(l.Traffic.Minutes()/time.Hour), int(l.Traffic.Hours()/day)
	return Settings{
		PagesDomain: l.PagesDomain, MaxFiles: l.MaxFiles, MaxFileBytes: l.MaxFileBytes,
		MaxReleaseBytes: l.MaxReleaseBytes, CrawlMaxPages: l.Crawl.MaxPages, CrawlMaxBytes: l.Crawl.MaxBytes,
		ReleasesKeep: &keep, ReleasesMode: l.Releases.SweepMode(),
		TrafficEnabled: &traffic, TrafficMinuteHours: &minuteHours, TrafficHourDays: &hourDays,
	}
}

// day is how long hour rows' retention is counted in.
const day = 24 * time.Hour

// With is the config with saved settings over it.
func (c Config) With(s Settings) Config {
	c.PagesDomain = s.PagesDomain
	c.MaxFiles, c.MaxFileBytes, c.MaxReleaseBytes = s.MaxFiles, s.MaxFileBytes, s.MaxReleaseBytes
	c.Crawl.MaxPages, c.Crawl.MaxBytes = s.CrawlMaxPages, s.CrawlMaxBytes
	if s.ReleasesKeep != nil {
		keep := *s.ReleasesKeep
		c.Releases.KeepDefault = &keep
	}
	if s.ReleasesMode != "" {
		c.Releases.Mode = s.ReleasesMode
	}
	if s.TrafficEnabled != nil {
		on := *s.TrafficEnabled
		c.Traffic.Enabled = &on
	}
	if s.TrafficMinuteHours != nil && *s.TrafficMinuteHours > 0 {
		c.Traffic.MinuteRetention = time.Duration(*s.TrafficMinuteHours) * time.Hour
	}
	if s.TrafficHourDays != nil && *s.TrafficHourDays > 0 {
		c.Traffic.HourRetention = time.Duration(*s.TrafficHourDays) * day
	}
	return c
}

// Live is the configuration in force, swapped whole when a saved setting
// changes, and read from memory on every request.
type Live struct{ v atomic.Pointer[Config] }

// NewLive starts from c.
func NewLive(c Config) *Live {
	l := &Live{}
	l.v.Store(&c)
	return l
}

// Get is the configuration in force.
func (l *Live) Get() Config { return *l.v.Load() }

// Set replaces the configuration in force.
func (l *Live) Set(c Config) { l.v.Store(&c) }
