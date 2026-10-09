// Package maps keeps the basemap a deployment serves for street maps (#2068):
// the operator's settings, the regions extracted from an OpenStreetMap build
// into the operator's bucket, the background fetch that extracts them, and the
// read the public archive route and the maps knowledge page make.
//
// A map is an HTML asset that loads the runtime the portal serves under
// /portal/vendor/maplibre/ and reads one of these archives by byte range from
// /portal/maps/. No map view and no thumbnail calls a third party; only the
// fetch does, and only when an administrator saves a region.
package maps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// SettingsSection is the platform_settings row the maps settings live in.
const SettingsSection = "maps"

// DefaultMaxZoom is the deepest zoom a region is extracted to unless the
// operator says otherwise. The Protomaps build goes to 15; the map runtime
// draws the zoom-14 tiles at 15 and beyond without loss a reader would
// notice, and the contiguous United States is about 8.8 GB at 14 against
// 18.8 GB at 15.
const DefaultMaxZoom = 14

// Settings is the operator's maps configuration.
type Settings struct {
	// Enabled turns the maps surface on: the archive routes answer, the fetch
	// runs, and the knowledge page lists the regions. Off by default.
	Enabled bool `json:"enabled"`
	// S3Connection names the S3 connection archives are written to. Empty is
	// the managed-resources connection.
	S3Connection string `json:"s3_connection"`
	// Bucket is the bucket archives are written to. Empty is the
	// managed-resources bucket.
	Bucket string `json:"bucket"`
	// MaxZoom is the deepest zoom a fetch extracts.
	MaxZoom int `json:"max_zoom"`
	// SourceURL is the archive a fetch extracts from. Empty is the current
	// Protomaps daily build, resolved when each fetch starts.
	SourceURL string `json:"source_url"`

	UpdatedBy string    `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// DefaultSettings is what a deployment that never saved the section has.
func DefaultSettings() Settings {
	return Settings{MaxZoom: DefaultMaxZoom}
}

// Validate refuses settings a fetch could not act on, returning the reason
// or "".
func (s Settings) Validate() string {
	if s.MaxZoom < 1 || s.MaxZoom > pmtiles.MaxZoom {
		return fmt.Sprintf("max_zoom must be between 1 and %d", pmtiles.MaxZoom)
	}
	if s.SourceURL != "" {
		u, err := url.Parse(s.SourceURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "source_url must be an http or https URL of a .pmtiles archive, or empty for the current Protomaps build"
		}
	}
	return ""
}

// SettingsStore persists the section.
type SettingsStore interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings, author string) error
}

// PostgresSettings is the section's row in platform_settings.
type PostgresSettings struct {
	db *sql.DB
}

// NewPostgresSettings returns the settings store over db.
func NewPostgresSettings(db *sql.DB) *PostgresSettings {
	return &PostgresSettings{db: db}
}

// Get returns the stored settings, or the defaults when none were saved.
func (s *PostgresSettings) Get(ctx context.Context) (Settings, error) {
	var raw []byte
	out := DefaultSettings()
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`,
		SettingsSection).Scan(&raw, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading maps settings: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Settings{}, fmt.Errorf("decoding maps settings: %w", err)
	}
	return out, nil
}

// Set upserts the settings.
func (s *PostgresSettings) Set(ctx context.Context, in Settings, author string) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding maps settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO platform_settings (section, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (section) DO UPDATE SET
		   value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		SettingsSection, raw, author)
	if err != nil {
		return fmt.Errorf("storing maps settings: %w", err)
	}
	return nil
}

// Preset is a named region an operator can pick instead of drawing a box.
type Preset struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Bounds pmtiles.Bounds `json:"bounds"`
}

// DefaultPresetID is the preset the settings page offers first.
const DefaultPresetID = "united-states"

// presets is the short list of regions offered by name. Each is one box; a
// country whose territory is scattered is offered as its parts.
var presets = []Preset{
	{ID: DefaultPresetID, Name: "United States (contiguous)", Bounds: pmtiles.Bounds{MinLon: -125.0, MinLat: 24.4, MaxLon: -66.9, MaxLat: 49.4}},
	{ID: "alaska", Name: "Alaska", Bounds: pmtiles.Bounds{MinLon: -179.2, MinLat: 51.2, MaxLon: -129.9, MaxLat: 71.5}},
	{ID: "hawaii", Name: "Hawaii", Bounds: pmtiles.Bounds{MinLon: -160.3, MinLat: 18.9, MaxLon: -154.8, MaxLat: 22.3}},
	{ID: "puerto-rico", Name: "Puerto Rico", Bounds: pmtiles.Bounds{MinLon: -67.3, MinLat: 17.9, MaxLon: -65.2, MaxLat: 18.6}},
	{ID: "canada", Name: "Canada", Bounds: pmtiles.Bounds{MinLon: -141.0, MinLat: 41.7, MaxLon: -52.6, MaxLat: 83.1}},
	{ID: "mexico", Name: "Mexico", Bounds: pmtiles.Bounds{MinLon: -118.4, MinLat: 14.5, MaxLon: -86.7, MaxLat: 32.7}},
	{ID: "united-kingdom-ireland", Name: "United Kingdom and Ireland", Bounds: pmtiles.Bounds{MinLon: -10.7, MinLat: 49.8, MaxLon: 1.8, MaxLat: 60.9}},
	{ID: "europe", Name: "Europe", Bounds: pmtiles.Bounds{MinLon: -25.0, MinLat: 34.5, MaxLon: 45.0, MaxLat: 71.5}},
	{ID: "australia", Name: "Australia", Bounds: pmtiles.Bounds{MinLon: 112.9, MinLat: -43.7, MaxLon: 153.7, MaxLat: -10.6}},
}

// Presets returns the regions offered by name.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// PresetByID returns the preset with the id.
func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
