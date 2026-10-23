// Package caldav provides the platform's CalDAV connection kind (#2246): a
// toolkit that reads one calendar account per connection over CalDAV (RFC
// 4791), so the platform can answer which meetings happened, when, who
// attended and what they were about. Most mail servers that are not Google or
// Microsoft serve calendars only this way.
//
// It is read-only by design. The tools list calendars, list the events in a
// time range with recurring events expanded, and read one event; the kind
// sends PROPFIND, REPORT and GET and nothing else, so there is no PUT, DELETE
// or MKCALENDAR for a model or a script to reach. Event titles, descriptions
// and attendee names are written by whoever sent the invitation, and come back
// cleaned and kept apart from the platform's own fields (internal/untrusted).
//
// The protocol is internal/caldav; this package is the connection kind around
// it: configuration, the connection registry, the tools and the probe.
package caldav

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

const (
	// Kind is the connection-instance kind discriminator.
	Kind = "caldav"

	// errPrefix names this kind in the shared upstream policy's messages.
	errPrefix = "caldav"

	// DefaultMaxEvents caps the occurrences one calendar_events call returns.
	DefaultMaxEvents = 500
	// DefaultMaxRangeDays caps the time range one call may ask about.
	DefaultMaxRangeDays = 366
	// MaxMaxEvents is the most max_events may be set to.
	MaxMaxEvents = 5000
	// MaxMaxRangeDays is the most max_range_days may be set to: ten years.
	MaxMaxRangeDays = 3660
	// defaultRangeDays is how far past its start a range that names no end
	// reaches.
	defaultRangeDays = 7
)

// cfgKey* name this kind's own keys. The credential, timeout and TLS keys
// (auth_mode, username, password, credential, connect_timeout, call_timeout,
// tls_ca_bundle_pem, ...) are read by internal/upstreamauth from the same map.
const (
	cfgKeyURL          = "url"
	cfgKeyDescription  = "description"
	cfgKeyTimeZone     = "time_zone"
	cfgKeyMaxEvents    = "max_events"
	cfgKeyMaxRangeDays = "max_range_days"
)

// Config is one CalDAV connection.
type Config struct {
	ConnectionName string
	// URL is the account's address: the server root, the principal or the
	// calendar home. Discovery finds the calendars from any of the three.
	URL         string
	Description string
	// TimeZone is the zone a time written without one ("floating") is read
	// in, as an IANA name.
	TimeZone     string
	MaxEvents    int
	MaxRangeDays int
	// Upstream is the authentication and transport the shared HTTP policy
	// reads: basic auth for most servers, bearer or OAuth for a hosted one.
	Upstream upstreamauth.Config
}

// Location is the configured zone.
func (c Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// ParseConfig reads a connection from the map it is stored as, applying
// defaults, and validates it.
func ParseConfig(cfg map[string]any) (Config, error) {
	raw := strings.TrimSpace(cfgmap.String(cfg, cfgKeyURL))
	up, err := upstreamauth.Parse(Kind, errPrefix, raw, cfg)
	if err != nil {
		return Config{}, err //nolint:wrapcheck // the message carries this kind's prefix already
	}
	c := Config{
		URL:          raw,
		Description:  cfgmap.String(cfg, cfgKeyDescription),
		TimeZone:     cfgmap.StringDefault(cfg, cfgKeyTimeZone, "UTC"),
		MaxEvents:    int(cfgmap.Int64(cfg, cfgKeyMaxEvents, DefaultMaxEvents)),
		MaxRangeDays: int(cfgmap.Int64(cfg, cfgKeyMaxRangeDays, DefaultMaxRangeDays)),
		Upstream:     up,
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate refuses a configuration the kind cannot serve.
func (c Config) Validate() error {
	if err := c.validateURL(); err != nil {
		return err
	}
	if err := c.validateLimits(); err != nil {
		return err
	}
	if c.Upstream.AuthMode == upstreamauth.AuthModeOAuth2AuthorizationCode || c.Upstream.OAuth2.Grant == connoauth.GrantAuthorizationCode {
		return errors.New("caldav: the authorization_code grant is not offered on a caldav connection; " +
			"use basic auth with an app password, a bearer token, or the client_credentials grant")
	}
	if err := c.Upstream.Validate(); err != nil {
		return err //nolint:wrapcheck // prefixed by the shared policy
	}
	return nil
}

func (c Config) validateURL() error {
	if c.URL == "" {
		return errors.New("caldav: url is required")
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("caldav: url %q must be an http or https address", c.URL)
	}
	if u.User != nil {
		return errors.New("caldav: put the account's name and password in username and password, not in the url")
	}
	return nil
}

func (c Config) validateLimits() error {
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return fmt.Errorf("caldav: time_zone %q is not an IANA zone name", c.TimeZone)
	}
	if c.MaxEvents < 1 || c.MaxEvents > MaxMaxEvents {
		return fmt.Errorf("caldav: max_events must be between 1 and %d, not %d", MaxMaxEvents, c.MaxEvents)
	}
	if c.MaxRangeDays < 1 || c.MaxRangeDays > MaxMaxRangeDays {
		return fmt.Errorf("caldav: max_range_days must be between 1 and %d, not %d", MaxMaxRangeDays, c.MaxRangeDays)
	}
	return nil
}

// MultiConfig is every configured caldav connection.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig reads the instances the registry hands the kind's factory.
// An instance that does not parse is skipped with a warning, so one bad
// connection cannot stop the platform from starting.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) MultiConfig {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := ParseConfig(r)
		if err != nil {
			slog.Warn("skipping invalid connection instance", "kind", Kind, "instance", name, "error", err)
			continue
		}
		c.ConnectionName = name
		c.Upstream.ConnectionName = name
		parsed[name] = c
	}
	return MultiConfig{DefaultName: defaultName, Instances: parsed}
}

const ownConfigSchemaProperties = `
    "url": {"type": "string", "description": "The calendar account's address: the server root, the account's principal, or its calendar home. The calendars are found from any of the three. Required."},
    "time_zone": {"type": "string", "description": "IANA zone a time written without a zone is read in. Defaults to UTC."},
    "max_events": {"type": "integer", "description": "Most occurrences one calendar_events call returns (1-5000). Defaults to 500."},
    "max_range_days": {"type": "integer", "description": "Longest time range one call may ask about, in days (1-3660). Defaults to 366."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."}`

// ConfigSchemaJSON declares what a caldav connection's config takes.
var ConfigSchemaJSON = fmt.Sprintf(`{
  "type": "object",
  "required": ["url"],
  "properties": {%s,
    %s,
    %s
  }
}`, ownConfigSchemaProperties,
	strings.TrimSuffix(strings.TrimPrefix(upstreamauth.ConfigSchemaPropertiesJSON, "{"), "}"),
	strings.TrimSuffix(strings.TrimPrefix(connoauth.ConfigSchemaPropertiesJSON, "{"), "}"))
