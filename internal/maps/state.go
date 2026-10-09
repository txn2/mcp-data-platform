package maps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// Ready is a region a map can load: what the public listing and the
// knowledge page show.
type Ready struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	URL     string         `json:"url"`
	Bounds  pmtiles.Bounds `json:"bounds"`
	MinZoom int            `json:"min_zoom"`
	MaxZoom int            `json:"max_zoom"`
	Build   string         `json:"build"`
	Size    int64          `json:"size_bytes"`
}

// ArchivePath is where the public route serves a region's archive.
func ArchivePath(id string) string { return "/portal/maps/" + id + ".pmtiles" }

// RegionsPath is where the public route lists the ready regions.
const RegionsPath = "/portal/maps/regions"

// State is what the maps surface offers a map right now.
type State struct {
	Enabled bool    `json:"enabled"`
	Regions []Ready `json:"regions"`
}

// Reader reads what the maps surface offers a map, from the stores alone: the
// knowledge page's reconcile reads it at startup, before any service runs.
type Reader struct {
	Settings SettingsStore
	Regions  RegionStore
}

// NewReader returns the reader over db's stores, or nil without a database.
func NewReader(db *sql.DB) *Reader {
	if db == nil {
		return nil
	}
	return &Reader{Settings: NewPostgresSettings(db), Regions: NewPostgresRegions(db)}
}

// State returns whether maps are enabled and the regions a map can load.
// Disabled, it lists none.
func (rd *Reader) State(ctx context.Context) (State, error) {
	settings, err := rd.Settings.Get(ctx)
	if err != nil {
		return State{}, err //nolint:wrapcheck // the store's error names what it was reading
	}
	out := State{Enabled: settings.Enabled, Regions: make([]Ready, 0)}
	if !settings.Enabled {
		return out, nil
	}
	regions, err := rd.Regions.List(ctx)
	if err != nil {
		return State{}, err //nolint:wrapcheck // the store's error names what it was reading
	}
	for _, r := range regions {
		if r.Archive == nil {
			continue
		}
		a := r.Archive
		out.Regions = append(out.Regions, Ready{
			ID: r.ID, Name: r.Name, URL: ArchivePath(r.ID), Bounds: a.Bounds,
			MinZoom: a.MinZoom, MaxZoom: a.MaxZoom, Build: a.Build, Size: a.Size,
		})
	}
	return out, nil
}

// State returns whether maps are enabled and the regions a map can load.
func (s *Service) State(ctx context.Context) (State, error) {
	return (&Reader{Settings: s.d.Settings, Regions: s.d.Regions}).State(ctx)
}

// ErrUnavailable is an archive the public route does not serve: maps are
// off, or the region has no ready archive.
var ErrUnavailable = errors.New("maps: no such archive")

// ServedArchive returns the archive a ready region serves and the bucket
// client to read it with.
func (s *Service) ServedArchive(ctx context.Context, id string) (Archive, Objects, error) {
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return Archive{}, nil, err //nolint:wrapcheck // the store's error names what it was reading
	}
	if !settings.Enabled || !ValidRegionID(id) {
		return Archive{}, nil, ErrUnavailable
	}
	r, err := s.d.Regions.Get(ctx, id)
	if errors.Is(err, ErrRegionNotFound) || (err == nil && r.Archive == nil) {
		return Archive{}, nil, ErrUnavailable
	}
	if err != nil {
		return Archive{}, nil, err //nolint:wrapcheck // the store's error names what it was reading
	}
	bucket, err := s.d.Open(ctx, settings)
	if err != nil {
		return Archive{}, nil, fmt.Errorf("maps: opening the bucket: %w", err)
	}
	return *r.Archive, bucket.Objects, nil
}
